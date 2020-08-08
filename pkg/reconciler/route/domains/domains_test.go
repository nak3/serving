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
package domains

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/go-cmp/cmp"
	"knative.dev/pkg/apis"

	network "knative.dev/networking/pkg"
	"knative.dev/serving/pkg/apis/serving"
	"knative.dev/serving/pkg/gc"
	"knative.dev/serving/pkg/reconciler/route/config"
)

func testConfig() *config.Config {
	return &config.Config{
		Domain: &config.Domain{
			Domains: map[string]*config.LabelSelector{
				"example.com": {},
				"another-example.com": {
					Selector: map[string]string{"app": "prod"},
				},
			},
		},
		Network: &network.Config{
			DefaultIngressClass: "ingress-class-foo",
			DomainTemplate:      network.DefaultDomainTemplate,
		},
		GC: &gc.Config{
			StaleRevisionLastpinnedDebounce: 1 * time.Minute,
		},
	}
}

func TestDomainNameFromTemplate(t *testing.T) {
	type args struct {
		name string
	}
	tests := []struct {
		name     string
		template string
		args     args
		want     string
		wantErr  bool
		local    bool
	}{{
		name:     "Default",
		template: "{{.Name}}.{{.Namespace}}.{{.Domain}}",
		args:     args{name: "test-name"},
		want:     "test-name.default.example.com",
		local:    false,
	}, {
		name:     "Dash",
		template: "{{.Name}}-{{.Namespace}}.{{.Domain}}",
		args:     args{name: "test-name"},
		want:     "test-name-default.example.com",
		local:    false,
	}, {
		name:     "LocalDash",
		template: "{{.Name}}-{{.Namespace}}.{{.Domain}}",
		args:     args{name: "test-name"},
		want:     "test-name.default.svc.cluster.local",
		local:    true,
	}, {
		name:     "Short",
		template: "{{.Name}}.{{.Domain}}",
		args:     args{name: "test-name"},
		want:     "test-name.example.com",
		local:    false,
	}, {
		name:     "SuperShort",
		template: "{{.Name}}",
		args:     args{name: "test-name"},
		want:     "test-name",
		local:    false,
	}, {
		name:     "Annotations",
		template: `{{.Name}}.{{ index .Annotations "sub"}}.{{.Domain}}`,
		args:     args{name: "test-name"},
		want:     "test-name.mysub.example.com",
		local:    false,
	}, {
		name:     "Labels",
		template: `{{.Name}}.{{ index .Labels "bus"}}.{{.Domain}}`,
		args:     args{name: "test-name"},
		want:     "test-name.mybus.example.com",
		local:    false,
	}, {
		// This cannot get through our validation, but verify we handle errors.
		name:     "BadVarName",
		template: "{{.Name}}.{{.NNNamespace}}.{{.Domain}}",
		args:     args{name: "test-name"},
		wantErr:  true,
		local:    false,
	}}

	meta := metav1.ObjectMeta{
		SelfLink:  "/apis/serving/v1/namespaces/test/Routes/myapp",
		Name:      "myroute",
		Namespace: "default",
		Labels: map[string]string{
			"route": "myapp",
			"bus":   "mybus",
		},
		Annotations: map[string]string{
			"sub": "mysub",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cfg := testConfig()
			cfg.Network.DomainTemplate = tt.template
			ctx = config.ToContext(ctx, cfg)

			if tt.local {
				meta.Labels[serving.VisibilityLabelKey] = serving.VisibilityClusterLocal
			} else {
				delete(meta.Labels, serving.VisibilityLabelKey)
			}

			got, err := DomainNameFromTemplate(ctx, meta, tt.args.name, false)
			if (err != nil) != tt.wantErr {
				t.Errorf("DomainNameFromTemplate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("DomainNameFromTemplate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestURL(t *testing.T) {
	tests := []struct {
		name     string
		scheme   string
		domain   string
		Expected apis.URL
	}{{
		name:   "subdomain",
		scheme: HTTPScheme,
		domain: "current.svc.local.com",
		Expected: apis.URL{
			Scheme: "http",
			Host:   "current.svc.local.com",
		},
	}, {
		name:   "default target",
		scheme: HTTPScheme,
		domain: "example.com",
		Expected: apis.URL{
			Scheme: "http",
			Host:   "example.com",
		},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := *URL(tt.scheme, tt.domain), tt.Expected; !cmp.Equal(want, got) {
				t.Errorf("URL = %v, want: %v", got, want)
			}
		})
	}
}

func TestIsClusterLocal(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		want   bool
	}{
		{
			name:   "domain is public",
			domain: "k8s.io",
			want:   false,
		},
		{
			name:   "domain is cluster local",
			domain: "my-app.cluster.local",
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsClusterLocal(tt.domain); got != tt.want {
				t.Errorf("IsClusterLocal() = %v, want %v", got, tt.want)
			}
		})
	}
}
