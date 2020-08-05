/*
Copyright 2020 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package visibility

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apilabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	listers "k8s.io/client-go/listers/core/v1"
	networkinglisters "knative.dev/networking/pkg/client/listers/networking/v1alpha1"
	"knative.dev/serving/pkg/apis/serving"
	v1 "knative.dev/serving/pkg/apis/serving/v1"
	"knative.dev/serving/pkg/reconciler/route/domains"
	"knative.dev/serving/pkg/reconciler/route/traffic"
)

// Resolver resolves the visibility of traffic targets, based on both the Route and placeholder Services labels.
type Resolver struct {
	serviceLister listers.ServiceLister
	realmLister   networkinglisters.RealmLister
}

// NewResolver returns a new Resolver.
func NewResolver(sl listers.ServiceLister, rl networkinglisters.RealmLister) *Resolver {
	return &Resolver{serviceLister: sl, realmLister: rl}
}

func (b *Resolver) getServices(route *v1.Route) (map[string]*corev1.Service, error) {
	// List all the Services owned by this Route.
	currentServices, err := b.serviceLister.Services(route.Namespace).List(apilabels.SelectorFromSet(
		apilabels.Set{
			serving.RouteLabelKey: route.Name,
		},
	))
	if err != nil {
		return nil, err
	}

	serviceCopy := make(map[string]*corev1.Service, len(currentServices))
	for _, svc := range currentServices {
		serviceCopy[svc.Name] = svc.DeepCopy()
	}

	return serviceCopy, err
}

// visibility adds Domain name to visibility
func (b *Resolver) visibility(meta metav1.ObjectMeta) string {
	realmName := "default" // TODO from configmap?
	if rname := meta.Labels[serving.VisibilityLabelKey]; rname != "" {
		realmName = rname
	}
	return realmName
}

func trafficNames(route *v1.Route) sets.String {
	names := sets.NewString(traffic.DefaultTarget)
	for _, tt := range route.Spec.Traffic {
		names.Insert(tt.Tag)
	}
	return names
}

// GetVisibility returns a map from traffic target name to their corresponding netv1alpha1.IngressVisibility.
func (b *Resolver) GetVisibility(ctx context.Context, route *v1.Route) (map[string]string, error) {
	// Find out the default visibility of the Route.
	defaultVisibility := b.visibility(route.ObjectMeta)

	// Get all the placeholder Services to check for additional visibility settings.
	services, err := b.getServices(route)
	if err != nil {
		return nil, err
	}
	trafficNames := trafficNames(route)
	m := make(map[string]string, trafficNames.Len())
	for tt := range trafficNames {
		hostname, err := domains.HostnameFromTemplate(ctx, route.Name, tt)
		if err != nil {
			return nil, err
		}
		ttVisibility := defaultVisibility
		// Is there a visibility setting on the placeholder Service?
		if svc, ok := services[hostname]; ok {
			ttVisibility = b.visibility(svc.ObjectMeta)
		}
		// TODO: choose the lowest visibility?
		m[tt] = ttVisibility
	}
	return m, nil
}

/* TODO
func minVisibility(a, b netv1alpha1.IngressVisibility) netv1alpha1.IngressVisibility {
	if a == netv1alpha1.IngressVisibilityClusterLocal || b == netv1alpha1.IngressVisibilityClusterLocal {
		return netv1alpha1.IngressVisibilityClusterLocal
	}
	return a
}
*/
