/*
Copyright 2019 The Knative Authors.

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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"

	"knative.dev/pkg/apis"
	//	duckv1beta1 "knative.dev/pkg/apis/duck/v1beta1"
)

var metricCondSet = apis.NewLivingConditionSet(MetricConditionReady)

// GetGroupVersionKind implements OwnerRefable.
func (m *Metric) GetGroupVersionKind() schema.GroupVersionKind {
	return SchemeGroupVersion.WithKind("Metric")
}

// TODO
func (metric *MetricStatus) MarkReady() {
	metricCondSet.Manage(metric).MarkTrue(MetricConditionReady)
}

// MarkInactive marks the PA as inactive.
func (metric *MetricStatus) MarkNotReady(reason, message string) {
	metricCondSet.Manage(metric).MarkUnknown(MetricConditionReady, reason, message)
}

// MarkFailed marks the certificate as not ready.
func (cs *CertificateStatus) MarkFailed(reason, message string) {
	metricCondSet.Manage(metric).MarkFailed(MetricConditionReady, reason, message)
}

/*
func (metric *MetricStatus) duck() *duckv1beta1.Status {
	return (*duckv1beta1.Status)(&metric.Status)
}
*/
