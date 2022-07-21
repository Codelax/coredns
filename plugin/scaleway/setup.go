package coredns_scaleway

import (
	"context"
	"errors"
	"fmt"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/fall"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	domain "github.com/scaleway/scaleway-sdk-go/api/domain/v2beta1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

type Settings struct {
	AccessKey *string
	SecretKey *string
	ProjectID *string
	zones     []string
	Fall      fall.F
}

var log = clog.NewWithPlugin("scaleway")

func init() {
	plugin.Register("scaleway", setup)
}

func loadProfile(ctx context.Context) (*scw.Profile, error) {
	config, err := scw.LoadConfig()
	if err != nil {
		if errors.Is(err, scw.ConfigFileNotFoundError{}) {
			config = &scw.Config{}
		} else {
			return nil, fmt.Errorf("failed to load scw config: %w", err)
		}
	}

	envProfile := scw.LoadEnvProfile()

	activeProfile, err := config.GetActiveProfile()
	if err != nil {
		return nil, fmt.Errorf("failed to get scw active profile: %w", err)
	}

	profile := scw.MergeProfiles(activeProfile, envProfile)

	return profile, nil
}

func setup(c *caddy.Controller) error {
	settings, err := parse(c)
	if err != nil {
		return plugin.Error("scaleway", fmt.Errorf("failed to parse settings: %w", err))
	}

	opts := []scw.ClientOption(nil)
	if settings.AccessKey != nil && settings.SecretKey != nil {
		opts = append(opts, scw.WithAuth(*settings.AccessKey, *settings.SecretKey))
	}

	profile, err := loadProfile(context.Background())
	if err != nil {
		return plugin.Error("scaleway", fmt.Errorf("failed to load profile: %w", err))
	}
	opts = append(opts, scw.WithProfile(profile))

	client, err := scw.NewClient(opts...)
	if err != nil {
		return plugin.Error("scaleway", fmt.Errorf("failed to create scaleway client"))
	}

	domainAPI := domain.NewAPI(client)

	ctx, cancel := context.WithCancel(context.Background())

	s, err := New(ctx, domainAPI, settings.zones, settings.Fall)
	if err != nil {
		cancel()
		return plugin.Error("scaleway", fmt.Errorf("failed to init plugin: %w", err))
	}

	err = s.Run(ctx)
	if err != nil {
		cancel()
		return plugin.Error("scaleway", fmt.Errorf("failed to start ScwDNS refresh job: %w", err))
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		s.Next = next
		return s
	})
	c.OnShutdown(func() error { cancel(); return nil })

	return nil
}

func parse(c *caddy.Controller) (Settings, error) {
	s := Settings{}

	c.Next()

	args := c.RemainingArgs()
	for _, arg := range args {
		s.zones = append(s.zones, arg)
	}

	for c.NextBlock() {
		key := c.Val()
		if !c.NextArg() {
			return s, c.ArgErr()
		}
		value := c.Val()
		switch key {
		case "access_key":
			s.AccessKey = scw.StringPtr(value)
		case "secret_key":
			s.SecretKey = scw.StringPtr(value)
		case "project_id":
			s.ProjectID = scw.StringPtr(value)
		case "fallthrough":
			s.Fall.SetZonesFromArgs(append(c.RemainingArgs(), value))
		default:
			return s, c.Errf("unknown key: %s", key)
		}
	}
	return s, nil
}
