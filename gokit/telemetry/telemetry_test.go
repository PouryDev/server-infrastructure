package telemetry

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestDefaultAttrsProject(t *testing.T) {
	got := map[attribute.Key]string{}
	for _, kv := range defaultAttrs(Config{ServiceName: "shop-api", Project: "shop", ServiceVersion: "1"}) {
		got[kv.Key] = kv.Value.AsString()
	}
	for k, want := range map[attribute.Key]string{"service.name": "shop-api", "service.namespace": "shop", "service.version": "1"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestDefaultAttrsNoProject(t *testing.T) {
	for _, kv := range defaultAttrs(Config{ServiceName: "x"}) {
		if kv.Key == "service.namespace" {
			t.Fatalf("service.namespace must not be set without a project")
		}
	}
}
