package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/hat47x/eki-fabric/internal/control/operatorapi"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// startAdmitter assembles the Administrator side of ADR-0038 §6 at startup, or
// returns nil to leave the route hidden.
//
// The current version is read from the state store because admission is a
// transition: a version is only acceptable as the successor of the one already
// stored. Without that version there is nothing to succeed, so the route stays
// hidden rather than accepting a first version nobody approved.
func startAdmitter(ctx context.Context, store any, book *trustbundle.ApprovalBook, keys []string, log *slog.Logger) operatorapi.Admitter {
	if len(keys) == 0 || book == nil {
		return nil
	}
	bundles, ok := store.(statestore.TrustBundleStore)
	if !ok {
		log.Warn("trust bundle admission disabled: the state store keeps no versions")
		return nil
	}
	latest, err := bundles.LatestTrustBundle(ctx)
	var store2 *trustbundle.Store
	if errors.Is(err, statestore.ErrNotFound) {
		// A fresh deployment has no version yet. The route still opens: the first
		// version is admitted through it, without approvals, because the Operator
		// certificate it would need is issued by a CA the version itself lists
		// (ADR-0036 §9.2). The configured key is what makes it trusted.
		log.Info("trust bundle admission enabled for the first version")
		store2 = &trustbundle.Store{}
		admitter, err := admitterFor(store2, book, keys, log)
		if err != nil || admitter == nil {
			log.Warn("trust bundle admission disabled: the control key is not usable")
			return nil
		}
		admitter.state = bundles
		return admitter
	}
	if err != nil {
		log.Warn("trust bundle admission disabled: the versions could not be read", "error", err.Error())
		return nil
	}
	signed, err := trustbundle.FromRecord(latest)
	if err != nil {
		log.Warn("trust bundle admission disabled: the stored version is not usable", "error", err.Error())
		return nil
	}
	store2, err = trustbundle.NewStore(signed)
	if err != nil {
		log.Warn("trust bundle admission disabled: the stored version is not usable", "error", err.Error())
		return nil
	}
	admitter, err := admitterFor(store2, book, keys, log)
	if err != nil {
		log.Warn("trust bundle admission disabled: the control key is not usable", "error", err.Error())
		return nil
	}
	if admitter == nil {
		return nil
	}
	admitter.state = bundles
	log.Info("trust bundle admission enabled", "version", signed.Bundle.Version)
	return admitter
}
