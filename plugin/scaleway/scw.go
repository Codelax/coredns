package coredns_scaleway

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	domain "github.com/scaleway/scaleway-sdk-go/api/domain/v2beta1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

type ScwDNS struct {
	api *domain.API

	exportedZones zoneFiles
	zones         []string
	zoneMutex     sync.RWMutex

	Next plugin.Handler
	Fall fall.F
}

type zoneFiles map[string]*file.Zone

func (s *ScwDNS) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	qname := state.Name()

	zoneName := plugin.Zones(s.zones).Matches(qname)
	if zoneName == "" {
		return plugin.NextOrFailure(s.Name(), s.Next, ctx, w, r)
	}

	zone, ok := s.exportedZones[zoneName]
	if !ok || zone == nil {
		return dns.RcodeServerFailure, nil
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	var result file.Result

	s.zoneMutex.RLock()
	m.Answer, m.Ns, m.Extra, result = zone.Lookup(ctx, state, qname)
	s.zoneMutex.RUnlock()

	if len(m.Answer) == 0 && result != file.NoData && s.Fall.Through(qname) {
		return plugin.NextOrFailure(s.Name(), s.Next, ctx, w, r)
	}

	switch result {
	case file.Success:
	case file.NoData:
	case file.NameError:
		m.Rcode = dns.RcodeNameError
	case file.Delegation:
		m.Authoritative = false
	case file.ServerFailure:
		return dns.RcodeServerFailure, nil
	}

	_ = w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

func (s *ScwDNS) Name() string {
	return "scaleway"
}

func (s *ScwDNS) updateZones(ctx context.Context) error {
	errChan := make(chan error)

	for _, zone := range s.zones {
		go func(zoneName string) {
			resp, err := s.api.ExportRawDNSZone(&domain.ExportRawDNSZoneRequest{
				DNSZone: zoneName,
				Format:  domain.RawFormatBind,
			}, scw.WithContext(ctx))
			if err != nil {
				errChan <- fmt.Errorf("failed to list dns zone records: %w", err)
				return
			}

			zoneFile, err := file.Parse(resp.Content, zoneName, "zone", 1)
			if err != nil {
				errChan <- fmt.Errorf("failed to parse zone file: %w", err)
				return
			}

			s.zoneMutex.Lock()
			s.exportedZones[zoneName] = zoneFile
			s.zoneMutex.Unlock()
			errChan <- nil
		}(zone)
	}

	errs := []error(nil)
	for range s.zones {
		err := <-errChan
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) != 0 {
		return fmt.Errorf("failed to update zones: %q", errs)
	}
	return nil
}

func (s *ScwDNS) Run(ctx context.Context) error {
	if err := s.updateZones(ctx); err != nil {
		return err
	}
	go func() {
		delay := 1 * time.Minute
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			timer.Reset(delay)
			select {
			case <-ctx.Done():
				log.Debugf("Breaking out of ScwDNS update loop for %v: %v", s.zones, ctx.Err())
				return
			case <-timer.C:
				if err := s.updateZones(ctx); err != nil && ctx.Err() == nil /* Don't log error if ctx expired. */ {
					log.Errorf("Failed to update exportedZones %v: %v", s.zones, err)
				}
			}
		}
	}()
	return nil
}

func New(ctx context.Context, api *domain.API, zones []string, f fall.F) (*ScwDNS, error) {
	fqdnZones := make([]string, len(zones))
	for i, zone := range zones {
		fqdnZones[i] = dns.Fqdn(zone)
	}

	return &ScwDNS{
		api:           api,
		zones:         fqdnZones,
		exportedZones: make(zoneFiles, len(zones)),
		Fall:          f,
	}, nil
}
