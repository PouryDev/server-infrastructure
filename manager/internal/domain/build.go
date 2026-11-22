package domain

// BuildSource is an owner-registered local project directory. Browsers choose an
// ID from the list; they never provide filesystem paths.
type BuildSource struct {
	ID            string      `json:"id"`
	Subdir        string      `json:"subdir"`
	App           string      `json:"app"`
	ComposeFile   string      `json:"composeFile"`
	Platform      string      `json:"platform"`
	KeepTags      int         `json:"keepTags"`
	Images        []BuildSpec `json:"images"`
	BuildArgNames []string    `json:"buildArgNames"`
	Services      []string    `json:"services"`
	Error         string      `json:"error,omitempty"`
}
type BuildSpec struct {
	Service    string `json:"service"`
	Context    string `json:"context"`
	Dockerfile string `json:"dockerfile"`
}
type BuildInspection struct {
	Source BuildSource `json:"source"`
	Head   string      `json:"head"`
	Dirty  bool        `json:"dirty"`
	Tag    string      `json:"tag"`
}

// BuildInfo is the value-free build description shown in a preview.
type BuildInfo struct {
	Source        string      `json:"source"`
	App           string      `json:"app"`
	Tag           string      `json:"tag"`
	Head          string      `json:"head"`
	Dirty         bool        `json:"dirty"`
	Platform      string      `json:"platform"`
	Images        []BuildSpec `json:"images"`
	BuildArgNames []string    `json:"buildArgNames"`
	Unbuilt       []string    `json:"unbuilt"`
}

type BuildRequest struct {
	Source   string            `json:"source"`
	Tag      string            `json:"tag"`
	Head     string            `json:"head"`
	Dirty    bool              `json:"dirty"`
	Services []string          `json:"services"`
	Args     map[string]string `json:"args"`
}
type BuiltImage struct {
	Service string `json:"service"`
	Ref     string `json:"ref"`
	ID      string `json:"id"`
}
