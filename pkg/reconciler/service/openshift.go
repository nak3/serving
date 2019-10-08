package service

import (
	"context"
	"go.uber.org/zap"
	"knative.dev/serving/pkg/apis/serving/v1alpha1"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/client-go/route/clientset/versioned"
	routeinformers "github.com/openshift/client-go/route/informers/externalversions"
	routelisters "github.com/openshift/client-go/route/listers/route/v1"
	"k8s.io/client-go/rest"
)

const (
	routeNamespace = "istio-system"
	//defaultResyncDuration = 10 * time.Minut
	defaultResyncDuration = 1 * time.Second
)

func (c *Reconciler) checkOpenShiftRoutesNotReady(ctx context.Context, logger *zap.SugaredLogger, service *v1alpha1.Service) {
	routeNamespaceLister := c.shiftRouteLister.Routes(routeNamespace)
	route, err := routeNamespaceLister.Get(service.Name)
	if err != nil {
		logger.Errorf("failed to get route: %v", err)
		service.Status.MarkRouteNotYetReady()
		return
	}
	for _, ingress := range route.Status.Ingress {
		if condition := findCondition(&ingress, routev1.RouteAdmitted); condition != nil {
			return
		}
	}
	logger.Errorf("route %q is still not admitted", service.Name)
	service.Status.MarkRouteNotYetReady()
}

// findCondition locates the first condition that corresponds to the requested type.
func findCondition(ingress *routev1.RouteIngress, t routev1.RouteIngressConditionType) (_ *routev1.RouteIngressCondition) {
	for i, existing := range ingress.Conditions {
		if existing.Type != t {
			continue
		}
		return &ingress.Conditions[i]
	}
	return nil
}

func shiftRouteLister() routelisters.RouteLister {
	c, err := rest.InClusterConfig()
	if err != nil {
		panic(err)
	}
	client, err := versioned.NewForConfig(c)
	if err != nil {
		panic(err)
	}

	factory := routeinformers.NewSharedInformerFactory(client, defaultResyncDuration)
	return factory.Route().V1().Routes().Lister()
}
