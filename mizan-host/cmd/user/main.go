// Command user manages the people who may sign in. There is no self-service sign-up: the first admin and every
// later account is created here.
//
//	user create --username m.ahmadi --name "Maryam Ahmadi" [--title "Chief accountant"] [--role admin|finance|user]
//	user set-password --username m.ahmadi
//	user list
//
// The password is read from the USER_PASSWORD environment variable; when it is not set a random one is generated
// and printed once. It is never a default and never part of the image.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"time"

	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"

	pgadapter "github.com/PouryDev/mizan-host/internal/adapter/postgres"
	"github.com/PouryDev/mizan-host/internal/domain/user"
	"github.com/PouryDev/mizan-host/internal/usecase/users"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "user:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: user create|set-password|list")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	db, err := postgres.Connect(ctx, postgres.Config{URL: url, StartupTimeout: 15 * time.Second})
	if err != nil {
		return err
	}
	defer db.Close()
	repo, err := pgadapter.NewUserRepository(db)
	if err != nil {
		return err
	}
	svc := users.New(repo, kitauth.NewArgon2id())

	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	username := fs.String("username", "", "username")
	name := fs.String("name", "", "display name")
	title := fs.String("title", "", "job title")
	role := fs.String("role", string(user.RoleAdmin), "admin | finance | user")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch args[0] {
	case "create":
		pw, generated := password()
		if *name == "" {
			*name = *username
		}
		u, err := svc.Create(ctx, users.CreateInput{Username: *username, Name: *name, Title: *title, Password: pw, Role: user.Role(*role)})
		if err != nil {
			return err
		}
		fmt.Printf("created %s (role %s)\n", u.Username, u.Role)
		printGenerated(generated, pw)
	case "set-password":
		pw, generated := password()
		if err := svc.SetPassword(ctx, *username, pw); err != nil {
			return err
		}
		fmt.Printf("password of %s changed\n", *username)
		printGenerated(generated, pw)
	case "list":
		page, err := svc.List(ctx, query.Params{})
		if err != nil {
			return err
		}
		for _, u := range page.Items {
			fmt.Printf("%-24s %-8s %s\n", u.Username, u.Role, u.Name)
		}
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}

func password() (pw string, generated bool) {
	if p := os.Getenv("USER_PASSWORD"); p != "" {
		return p, false
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	b := make([]byte, 20)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err) // the system's random source is broken: nothing safe can be done
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), true
}

func printGenerated(generated bool, pw string) {
	if generated {
		fmt.Println("generated password (shown once):", pw)
	}
}
