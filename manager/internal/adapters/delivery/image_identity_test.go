package delivery

import (
	"strings"
	"testing"
)

func TestPortableImageIdentityVerifiesContentAcrossDockerStores(t *testing.T) {
	raw := `{"Id":"sha256:` + strings.Repeat("a", 64) + `","Os":"linux","Architecture":"amd64","Config":{"User":"65532","Env":["KEY=value"]},"RootFS":{"Type":"layers","Layers":["sha256:layer-one"]}}`
	first, err := parseImage([]byte(raw), "app-api:one")
	if err != nil {
		t.Fatal(err)
	}
	otherStore := strings.Replace(raw, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	second, err := parseImage([]byte(otherStore), "app-api:one")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.ContentID != second.ContentID {
		t.Fatal("storage-specific identity was used instead of content")
	}
	classicScratch := strings.Replace(otherStore, `"User":"65532"`, `"Cmd":null,"Labels":null,"Volumes":null,"OnBuild":null,"User":"65532"`, 1)
	scratch, err := parseImage([]byte(classicScratch), "app-api:one")
	if err != nil || scratch.ContentID != first.ContentID {
		t.Fatal("unset OCI fields changed scratch image identity", err)
	}
	for _, changed := range []string{strings.Replace(raw, "layer-one", "layer-two", 1), strings.Replace(raw, "KEY=value", "KEY=modified", 1)} {
		image, err := parseImage([]byte(changed), "app-api:one")
		if err != nil {
			t.Fatal(err)
		}
		if image.ContentID == first.ContentID {
			t.Fatal("modified runtime configuration or filesystem accepted")
		}
	}
}
