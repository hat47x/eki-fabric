package service

import (
	"context"
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
	if err != nil {
		// A fresh deployment has no version yet, and that is not a failure: the
		// first version is written by hand, not admitted.
		log.Info("trust bundle admission disabled: no version stored yet", "error", err.Error())
		return nil
	}
	signed, err := trustbundle.FromRecord(latest)
	if err != nil {
		log.Warn("trust bundle admission disabled: the stored version is not usable", "error", err.Error())
		return nil
	}
	store2, err := trustbundle.NewStore(signed)
	if err != nil {
		log.Warn("trust bundle admission disabled: the stored version is not usable", "error", err.Error())
		return nil
	}
	admitter, err := admitterFor(store2, book, keys)
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
