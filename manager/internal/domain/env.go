package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Project env management. The destination server's files are the store: the
// project-wide .env and one optional env.d/<service>.env per service.
const (
	MaxEnvValueBytes = 16 << 10
	MaxEnvChanges    = 200
	EnvBackupsKept   = 10
)

var (
	projectNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	envNamePattern     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	servicePattern     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,99}$`)
	sensitiveEnvName   = regexp.MustCompile(`(KEY|SECRET|TOKEN|PASSWORD|PASSWD|PASS|DSN|CREDENTIAL|PRIVATE|CERT|(^|_)(URL|URI)$)`)
)

// ValidProject accepts only names the provisioning scripts could have created.
// The shared infrastructure project is never an env/apply target.
func ValidProject(name string) bool { return projectNamePattern.MatchString(name) && name != "infra" }
func ValidService(name string) bool { return servicePattern.MatchString(name) }
func ValidEnvName(name string) bool { return len(name) <= 128 && envNamePattern.MatchString(name) }

// OneShotService reports whether a Compose service is a migration/seed/setup job
// that must never be recreated by an env apply. The words mirror the lifecycle
// manager's protection rule.
func OneShotService(name, restart string, labels map[string]string) bool {
	lower := strings.ToLower(name)
	for _, word := range []string{"migrate", "migration", "seed", "setup", "init"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return restart == "no" || strings.EqualFold(labels["infra.manager.managed"], "false")
}

func SensitiveEnvName(name string) bool { return sensitiveEnvName.MatchString(name) }

// EnvChange is one reviewed edit. Scope "" is the whole project; otherwise a service.
type EnvChange struct {
	Scope  string `json:"scope"`
	Name   string `json:"name"`
	Action string `json:"action"`
	Value  string `json:"value,omitempty"`
}

func (c EnvChange) Validate() error {
	if c.Scope != "" && !ValidService(c.Scope) {
		return fmt.Errorf("invalid environment scope")
	}
	if !ValidEnvName(c.Name) {
		return fmt.Errorf("variable names must match [A-Z_][A-Z0-9_]*")
	}
	switch c.Action {
	case "set":
		if len(c.Value) > MaxEnvValueBytes {
			return fmt.Errorf("environment value is too large")
		}
		if strings.ContainsAny(c.Value, "\n\r\x00") {
			return fmt.Errorf("environment values cannot contain newlines or NUL")
		}
	case "delete":
		if c.Value != "" {
			return fmt.Errorf("a delete cannot carry a value")
		}
	default:
		return fmt.Errorf("invalid environment action")
	}
	return nil
}

func ValidateEnvChanges(changes []EnvChange) error {
	if len(changes) > MaxEnvChanges {
		return fmt.Errorf("too many environment changes")
	}
	seen := map[string]bool{}
	for _, c := range changes {
		if err := c.Validate(); err != nil {
			return err
		}
		key := c.Scope + "\x00" + c.Name
		if seen[key] {
			return fmt.Errorf("a variable appears twice in one request")
		}
		seen[key] = true
	}
	return nil
}

// EnvChangeSummary is the value-free view of a change used in previews and audit.
type EnvChangeSummary struct {
	Scope  string `json:"scope"`
	Name   string `json:"name"`
	Action string `json:"action"`
	// Exists tells an update from an addition; it is filled from the destination.
	Exists bool `json:"exists"`
}

func SummarizeEnv(changes []EnvChange) []EnvChangeSummary {
	out := make([]EnvChangeSummary, 0, len(changes))
	for _, c := range changes {
		out = append(out, EnvChangeSummary{Scope: c.Scope, Name: c.Name, Action: c.Action})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// AuditEnvDetail lists only scopes, actions and variable NAMES.
func AuditEnvDetail(changes []EnvChange) string {
	groups := map[string][]string{}
	for _, c := range changes {
		label := c.Scope
		if label == "" {
			label = "project"
		}
		groups[label+" "+c.Action] = append(groups[label+" "+c.Action], c.Name)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		names := groups[k]
		sort.Strings(names)
		parts = append(parts, k+"="+strings.Join(names, ","))
	}
	return strings.Join(parts, "; ")
}

// Raw (unmasked) state is exchanged only on the authenticated runner protocol.
type EnvPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type EnvFileRaw struct {
	Scope     string    `json:"scope"`
	Exists    bool      `json:"exists"`
	Variables []EnvPair `json:"variables"`
}
type EnvServiceInfo struct {
	Name           string `json:"name"`
	OneShot        bool   `json:"oneShot"`
	UsesProjectEnv bool   `json:"usesProjectEnv"`
	HasOverride    bool   `json:"hasOverride"`
}
type EnvStateRaw struct {
	Project  string           `json:"project"`
	Revision string           `json:"revision"`
	Services []EnvServiceInfo `json:"services"`
	Files    []EnvFileRaw     `json:"files"`
}
type EnvProject struct {
	Name     string   `json:"name"`
	Services []string `json:"services"`
}

// Masked state is the only form returned by list endpoints.
type EnvVariable struct {
	Name      string `json:"name"`
	Masked    bool   `json:"masked"`
	Sensitive bool   `json:"sensitive"`
	Empty     bool   `json:"empty"`
}
type EnvFile struct {
	Scope     string        `json:"scope"`
	Exists    bool          `json:"exists"`
	Variables []EnvVariable `json:"variables"`
}
type EnvState struct {
	Project  string           `json:"project"`
	Revision string           `json:"revision"`
	Services []EnvServiceInfo `json:"services"`
	Files    []EnvFile        `json:"files"`
}

func MaskEnv(raw EnvStateRaw) EnvState {
	out := EnvState{Project: raw.Project, Revision: raw.Revision, Services: raw.Services, Files: []EnvFile{}}
	if out.Services == nil {
		out.Services = []EnvServiceInfo{}
	}
	for _, f := range raw.Files {
		file := EnvFile{Scope: f.Scope, Exists: f.Exists, Variables: []EnvVariable{}}
		for _, v := range f.Variables {
			file.Variables = append(file.Variables, EnvVariable{Name: v.Name, Masked: true, Sensitive: SensitiveEnvName(v.Name), Empty: v.Value == ""})
		}
		out.Files = append(out.Files, file)
	}
	return out
}

// EnvWriteRequest is a reviewed edit set tied to the revision the user saw.
type EnvWriteRequest struct {
	Revision string      `json:"revision"`
	Changes  []EnvChange `json:"changes"`
}
type EnvRevealRequest struct {
	Scope string `json:"scope"`
	Name  string `json:"name"`
}
