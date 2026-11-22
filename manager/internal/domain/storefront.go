package domain

type StorefrontSpec struct {
	Accent      string   `json:"accent,omitempty"`
	SMSKey      string   `json:"smsKey,omitempty"`
	SMSTemplate string   `json:"smsTemplate,omitempty"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Domain      string   `json:"domain"`
	Title       string   `json:"title"`
	Theme       string   `json:"theme"`
	OwnerPhone  string   `json:"ownerPhone"`
	AdminPhone  string   `json:"adminPhone"`
	Demo        bool     `json:"demo"`
	Image       string   `json:"image"`
	Modules     []string `json:"modules"`
}
type StorefrontJob struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Domain    string `json:"domain"`
	Status    string `json:"status"`
	Phase     string `json:"phase"`
	Message   string `json:"message"`
	URL       string `json:"url"`
	CreatedAt int64  `json:"createdAt"`
}
type StorefrontTheme struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Layout     string `json:"layout"`
	Accent     string `json:"accent"`
	Background string `json:"background"`
}
type StorefrontModule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type StorefrontCatalog struct {
	Images  []string           `json:"images"`
	Themes  []StorefrontTheme  `json:"themes"`
	Modules []StorefrontModule `json:"modules"`
	Jobs    []StorefrontJob    `json:"jobs"`
}
