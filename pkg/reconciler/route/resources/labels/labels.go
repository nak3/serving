/*
Copyright 2019 The Knative Authors

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

package labels

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SetLabel sets/update the label of the an ObjectMeta
func SetLabel(meta *v1.ObjectMeta, key string, value string) {
	if meta.Labels == nil {
		meta.Labels = make(map[string]string, 1)
	}

	meta.Labels[key] = value
}

// DeleteLabel removes a label from the ObjectMeta
func DeleteLabel(meta *v1.ObjectMeta, key string) {
	delete(meta.Labels, key)
}
