/*
Copyright 2018 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package route

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	netv1alpha1 "knative.dev/networking/pkg/apis/networking/v1alpha1"
	"knative.dev/pkg/apis/duck"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	v1 "knative.dev/serving/pkg/apis/serving/v1"
	"knative.dev/serving/pkg/reconciler/route/config"
	"knative.dev/serving/pkg/reconciler/route/resources"
	"knative.dev/serving/pkg/reconciler/route/traffic"
)

func (c *Reconciler) reconcileIngress(ctx context.Context, r *v1.Route, desired *netv1alpha1.Ingress) (*netv1alpha1.Ingress, error) {
	recorder := controller.GetEventRecorder(ctx)
	ingress, err := c.ingressLister.Ingresses(desired.Namespace).Get(desired.Name)
	if apierrs.IsNotFound(err) {
		ingress, err = c.netclient.NetworkingV1alpha1().Ingresses(desired.Namespace).Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			recorder.Eventf(r, corev1.EventTypeWarning, "CreationFailed", "Failed to create Ingress: %v", err)
			return nil, fmt.Errorf("failed to create Ingress: %w", err)
		}

		recorder.Eventf(r, corev1.EventTypeNormal, "Created", "Created Ingress %q", ingress.GetName())
		return ingress, nil
	} else if err != nil {
		return nil, err
	} else if !equality.Semantic.DeepEqual(ingress.Spec, desired.Spec) ||
		!equality.Semantic.DeepEqual(ingress.Annotations, desired.Annotations) ||
		!equality.Semantic.DeepEqual(ingress.Labels, desired.Labels) {
		// It is notable that one reason for differences here may be defaulting.
		// When that is the case, the Update will end up being a nop because the
		// webhook will bring them into alignment and no new reconciliation will occur.
		// Also, compare annotation and label in case ingress.Class or parent route's labels
		// is updated.

		// Don't modify the informers copy
		origin := ingress.DeepCopy()
		origin.Spec = desired.Spec
		origin.Annotations = desired.Annotations
		origin.Labels = desired.Labels
		updated, err := c.netclient.NetworkingV1alpha1().Ingresses(origin.Namespace).Update(ctx, origin, metav1.UpdateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to update Ingress: %w", err)
		}
		return updated, nil
	}

	return ingress, err
}

func (c *Reconciler) deleteServices(ctx context.Context, namespace string, serviceNames sets.String) error {
	for _, serviceName := range serviceNames.List() {
		if err := c.kubeclient.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{}); err != nil {
			return fmt.Errorf("failed to delete Service: %w", err)
		}
	}

	return nil
}

func (c *Reconciler) reconcilePlaceholderServices(ctx context.Context, route *v1.Route, targets map[string]traffic.RevisionTargets) ([]*corev1.Service, error) {
	logger := logging.FromContext(ctx)
	recorder := controller.GetEventRecorder(ctx)

	existingServices, err := c.getServices(route)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch existing services: %w", err)
	}
	existingServiceNames := resources.GetNames(existingServices)

	ns := route.Namespace
	names := make(sets.String, len(targets))
	for name := range targets {
		names.Insert(name)
	}

	services := make([]*corev1.Service, 0, names.Len())
	createdServiceNames := make(sets.String, names.Len())
	for _, name := range names.List() {
		desiredService, err := resources.MakeK8sPlaceholderService(ctx, route, name)
		if err != nil {
			return nil, fmt.Errorf("failed to construct placeholder k8s service: %w", err)
		}

		service, err := c.serviceLister.Services(ns).Get(desiredService.Name)
		if apierrs.IsNotFound(err) {
			// Doesn't exist, create it.
			service, err = c.kubeclient.CoreV1().Services(ns).Create(ctx, desiredService, metav1.CreateOptions{})
			if err != nil {
				recorder.Eventf(route, corev1.EventTypeWarning, "CreationFailed",
					"Failed to create placeholder service %q: %v", desiredService.Name, err)
				return nil, fmt.Errorf("failed to create placeholder service: %w", err)
			}
			logger.Info("Created service ", desiredService.Name)
			recorder.Eventf(route, corev1.EventTypeNormal, "Created", "Created placeholder service %q", desiredService.Name)
		} else if err != nil {
			return nil, err
		} else if !metav1.IsControlledBy(service, route) {
			// Surface an error in the route's status, and return an error.
			route.Status.MarkServiceNotOwned(desiredService.Name)
			return nil, fmt.Errorf("route: %q does not own Service: %q", route.Name, desiredService.Name)
		}

		services = append(services, service)
		createdServiceNames.Insert(desiredService.Name)
	}

	// Delete any current services that was no longer desired.
	if err := c.deleteServices(ctx, ns, existingServiceNames.Difference(createdServiceNames)); err != nil {
		return nil, err
	}

	// TODO(mattmoor): This is where we'd look at the state of the Service and
	// reflect any necessary state into the Route.
	return services, nil
}

func (c *Reconciler) updatePlaceholderServices(ctx context.Context, route *v1.Route, services []*corev1.Service, ingress *netv1alpha1.Ingress) error {
	eg, _ := errgroup.WithContext(ctx)
	for _, service := range services {
		service := service

		ingressStatus := ingress.Status

		lbStatus := ingressStatus.PublicLoadBalancer
		if resources.IsClusterLocalService(service) || ingressStatus.PrivateLoadBalancer != nil {
			// Always use private load balancer if it exists,
			// because k8s service is only useful for inter-cluster communication.
			// External communication will be handle via ingress gateway, which won't be affected by what is configured here.
			lbStatus = ingressStatus.PrivateLoadBalancer
		}

		if lbStatus == nil || len(lbStatus.Ingress) == 0 {
			//return errLoadBalancerNotFound
			return nil
		}
		if len(lbStatus.Ingress) > 1 {
			// Return error as we only support one LoadBalancer currently.
			return errors.New(
				"more than one ingress are specified in status(LoadBalancer) of Ingress " + ingress.GetName())
		}
		balancer := lbStatus.Ingress[0]
		eg.Go(func() error {
			switch {
			case balancer.DomainInternal != "":
				if err := c.reconcileEndpoints(ctx, balancer.DomainInternal, service, route); err != nil {
					return err
				}
				return c.reconcilePlaceholderServiceSpec(ctx, balancer.DomainInternal, service, route)
			case balancer.Domain != "":
				if err := c.reconcileEndpoints(ctx, balancer.Domain, service, route); err != nil {
					return err
				}
				return c.reconcilePlaceholderServiceSpec(ctx, balancer.Domain, service, route)
			case balancer.MeshOnly:
				// No need to update Placeholderr service.
			case balancer.IP != "":
				// TODO(lichuqiang): deal with LoadBalancer IP.
				// We'll also need ports info to make it take effect.
			}
			return nil
		})
	}

	// TODO(mattmoor): This is where we'd look at the state of the Service and
	// reflect any necessary state into the Route.
	return eg.Wait()
}

func (c *Reconciler) reconcileEndpoints(ctx context.Context, ingress string, service *corev1.Service, route *v1.Route) error {
	parts := strings.Split(ingress, ".")
	name, namespace := parts[0], parts[1]

	// Get Ingress's endpoints.
	ingEp, err := c.endpointsLister.Endpoints(namespace).Get(name)
	if err != nil {
		return fmt.Errorf("failed to find ingress endpoints: %w", err)
	}

	// Copy ingress endpoints' subsets to local endpoints.
	desiredEp := resources.MakeEndpoints(ctx, service, route, ingEp)

	// Create or update local ingress endpoint.
	localEp, err := c.endpointsLister.Endpoints(service.Namespace).Get(service.Name)
	if apierrs.IsNotFound(err) {
		if _, err = c.kubeclient.CoreV1().Endpoints(service.Namespace).Create(ctx, desiredEp, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("failed to create local endpoints: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get local endpoints: %w", err)
	} else {
		// Make sure that the service has the proper specification.
		if !equality.Semantic.DeepEqual(localEp.Subsets, desiredEp.Subsets) {
			// Don't modify the informers copy
			existing := localEp.DeepCopy()
			existing.Subsets = desiredEp.Subsets
			if _, err = c.kubeclient.CoreV1().Endpoints(service.Namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Reconciler) reconcilePlaceholderServiceSpec(ctx context.Context, ingress string, service *corev1.Service, route *v1.Route) error {
	parts := strings.Split(ingress, ".")
	name, namespace := parts[0], parts[1]

	ingService, err := c.serviceLister.Services(namespace).Get(name)
	if err != nil {
		return fmt.Errorf("failed to find ingress service: %w", err)
	}

	desiredSpec := ingService.Spec
	desiredSpec.Type = corev1.ServiceTypeClusterIP
	desiredSpec.SessionAffinity = corev1.ServiceAffinityNone

	// Make sure that the service has the proper specification.
	if !equality.Semantic.DeepEqual(service.Spec, desiredSpec) {
		// Don't modify the informers copy.
		existing := service.DeepCopy()
		existing.Spec = desiredSpec
		if _, err := c.kubeclient.CoreV1().Services(service.Namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return nil
}

// Update the lastPinned annotation on revisions we target so they don't get GC'd.
func (c *Reconciler) reconcileTargetRevisions(ctx context.Context, t *traffic.Config, route *v1.Route) error {
	gcConfig := config.FromContext(ctx).GC
	logger := logging.FromContext(ctx)
	lpDebounce := gcConfig.StaleRevisionLastpinnedDebounce

	eg, egCtx := errgroup.WithContext(ctx)
	for _, target := range t.Targets {
		for _, rt := range target {
			tt := rt.TrafficTarget
			eg.Go(func() error {
				rev, err := c.revisionLister.Revisions(route.Namespace).Get(tt.RevisionName)
				if apierrs.IsNotFound(err) {
					logger.Infof("Unable to update lastPinned for missing revision %q", tt.RevisionName)
					return nil
				} else if err != nil {
					return err
				}

				newRev := rev.DeepCopy()

				lastPin, err := newRev.GetLastPinned()
				if err != nil {
					// Missing is an expected error case for a not yet pinned revision.
					var errLastPinned v1.LastPinnedParseError
					if errors.As(err, &errLastPinned) && errLastPinned.Type != v1.AnnotationParseErrorTypeMissing {
						return err
					}
				} else if lastPin.Add(lpDebounce).After(c.clock.Now()) {
					// Enforce a delay before performing an update on lastPinned to avoid excess churn.
					return nil
				}

				newRev.SetLastPinned(c.clock.Now())

				patch, err := duck.CreateMergePatch(rev, newRev)
				if err != nil {
					return err
				}

				if _, err := c.client.ServingV1().Revisions(route.Namespace).Patch(egCtx, rev.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
					return fmt.Errorf("failed to set revision annotation: %w", err)
				}
				return nil
			})
		}
	}
	return eg.Wait()
}
