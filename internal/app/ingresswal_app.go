package app

import "github.com/rknightion/tailscale2otel/v5/internal/ingresswal"

func (a *App) buildIngressWAL(routes []ingressWALRoute) error {
	if !a.cfg.IngressWAL.Enabled {
		coordinator, err := newIngressWALCoordinator(nil, nil)
		if err != nil {
			return err
		}
		a.ingressWAL = coordinator
		return nil
	}

	store, err := ingresswal.New(ingresswal.Options{
		Directory:  a.cfg.IngressWAL.Directory,
		MaxBytes:   a.cfg.IngressWAL.MaxBytes,
		MaxEntries: a.cfg.IngressWAL.MaxEntries,
	})
	if err != nil {
		return err
	}
	coordinator, err := newIngressWALCoordinator(store, routes)
	if err != nil {
		_ = store.Close()
		return err
	}
	a.ingressWAL = coordinator
	return nil
}
