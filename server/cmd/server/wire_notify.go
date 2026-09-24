package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/welcometotheweb/ourway-rmm/server/internal/notify"
	"github.com/welcometotheweb/ourway-rmm/server/internal/store"
)

// wireNotify sets up the notification channel + policy framework (gap #6).
// Returns the store and sender for the HTTP API. When Postgres is not
// available (in-memory mode), returns nils (503).
func wireNotify(hasPG bool, pgPool *pgxpool.Pool) (store.NotifyStore, *notify.Sender, *notify.Router) {
	if !hasPG {
		return nil, nil, nil
	}
	nstore := store.NewPostgresNotifyStore(pgPool)
	sender := notify.NewSender(func(ctx context.Context, host, port, from, to, username, password, subject, body string) error {
		// SMTP send stub — real send is wired when SMTP config is available.
		return nil
	})
	// Load initial policies and build router.
	policies, _ := nstore.ListPolicies(context.Background())
	notifyPolicies := make([]*notify.Policy, 0, len(policies))
	for _, p := range policies {
		notifyPolicies = append(notifyPolicies, &notify.Policy{
			ID:       p.ID,
			Category: p.Category,
			ClientID: p.ClientID,
			Role:     p.Role,
			Channels: p.Channels,
			Enabled:  p.Enabled,
		})
	}
	// Load channels into sender.
	channels, _ := nstore.ListChannels(context.Background())
	sender.SetChannels(channels)
	router := notify.NewRouter(sender, notifyPolicies, log.New(os.Stderr, "notify: ", 0))
	log.Println("notify: Postgres-backed notification channels + policies wired")
	return nstore, sender, router
}
