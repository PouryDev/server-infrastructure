package shop

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The manager runner and the Go renderer must advertise the same working choices.
func TestManagerThemeCatalogMatchesRenderer(t *testing.T) {
	raw, err := os.ReadFile("../../../catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Themes []Theme `json:"themes"`
	}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(catalog.Themes, Themes()) {
		t.Fatal("manager catalog and renderer themes differ")
	}
}
