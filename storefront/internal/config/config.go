// Package config validates environment-only instance configuration.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	GapchiSecret string

	DatabaseURL, Origin, Secret, Uploads, Instance, OwnerPhone, OwnerPassword, AdminPhone, AdminPassword, SMSKey, SMSTemplate string
	Demo                                                                                                                      bool
}

func Load() (Config, error) {
	c := Config{GapchiSecret: os.Getenv("GAPCHI_IDENTITY_SECRET"), DatabaseURL: os.Getenv("DATABASE_URL"), Origin: os.Getenv("STORE_ORIGIN"), Secret: os.Getenv("STORE_SECRET"), Uploads: os.Getenv("STORE_UPLOADS"), Instance: os.Getenv("STORE_INSTANCE"), OwnerPhone: os.Getenv("STORE_OWNER_PHONE"), OwnerPassword: os.Getenv("STORE_OWNER_PASSWORD"), AdminPhone: os.Getenv("STORE_ADMIN_PHONE"), AdminPassword: os.Getenv("STORE_ADMIN_PASSWORD"), SMSKey: os.Getenv("KAVENEGAR_API_KEY"), SMSTemplate: os.Getenv("KAVENEGAR_TEMPLATE"), Demo: os.Getenv("STORE_DEMO") == "true"}
	u, e := url.Parse(c.Origin)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("STORE_ORIGIN must be an exact HTTP(S) origin")
	}
	if c.Demo && !strings.HasSuffix(u.Hostname(), ".localhost") {
		return c, fmt.Errorf("STORE_DEMO is restricted to .localhost")
	}
	if c.DatabaseURL == "" || len(c.Secret) < 32 || c.Instance == "" {
		return c, fmt.Errorf("DATABASE_URL, STORE_INSTANCE and STORE_SECRET (32+ chars) are required")
	}
	if !c.Demo && (c.SMSKey == "" || c.SMSTemplate == "") {
		return c, fmt.Errorf("production phone authentication requires SMS configuration")
	}
	if c.Uploads == "" {
		c.Uploads = "/data/uploads"
	}
	return c, nil
}
