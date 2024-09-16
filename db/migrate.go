package db

import (
	"embed"
	"fmt"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/rqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/golang-migrate/migrate/v4/source/iofs"
)

type URL struct {
	User     string
	Password string
	Host     string
	Port     int
	Secure   bool
}

func (c URL) DataSourceName() string {
	scheme := "http"
	if c.Secure {
		scheme = "https"
	}
	u := &url.URL{
		Scheme: scheme,
		User:   url.UserPassword(c.User, c.Password),
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
	}
	return u.String()
}

func (c URL) migrateDatabaseURL() string {
	u := &url.URL{
		Scheme: "rqlite",
		User:   url.UserPassword(c.User, c.Password),
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
	}
	if !c.Secure {
		q := u.Query()
		q.Set("x-connect-insecure", "true")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

//go:embed migrations/*.sql
var fs embed.FS

func Migrate(u URL) (err error) {
	srcDriver, err := iofs.New(fs, "migrations")
	if err != nil {
		return fmt.Errorf("db: migrate failed to create iofs: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", srcDriver, u.migrateDatabaseURL())
	if err != nil {
		return fmt.Errorf("db: migrate failed to create source instance: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("db: migrate up failed: %w", err)
	}
	return nil
}
