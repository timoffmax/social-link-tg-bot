package app

import (
	"context"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/services"
	"github.com/timoffmax/social-link-bot/services/botsettings"
)

// settingsManager is the settings goroutine: it loads the owner-set values
// before the startup checks (so the first profile push already follows
// them), then applies the dashboard's requests and reloads until shutdown.
var settingsManager *botsettings.Manager

// initSettings loads the owner-set values: without a database configured
// none (.env and the defaults, as before the dashboard); with one from the
// database, else (dbErr from InitDatabase, or a failed read) from the cache
// file, else none.
func initSettings(dbErr error) {
	var store botsettings.Store
	isInitialized := services.IsDBInitialized()

	if isInitialized == true {
		db := services.GetDB()
		store = botsettings.NewPGStore(db)
	}

	databaseErr := unavailableDatabaseErr(isInitialized, dbErr)
	options := botsettings.Options{
		Store:         store,
		DatabaseErr:   databaseErr,
		Notify:        services.NotifyAdminUnthrottled,
		PushProfile:   pushBotProfile,
		ApplyRequests: true,
		Approver:      newSettingsApprover(),
	}
	settingsManager = botsettings.New(options)
	ctx := context.Background()
	settingsManager.Load(ctx)
}

// unavailableDatabaseErr returns dbErr when a configured database could not
// be opened at start, so the settings fall back to the cache file; nil with
// a database, or with none configured (the settings then stay inert).
func unavailableDatabaseErr(isInitialized bool, dbErr error) error {
	var result error
	isUnavailable := (isInitialized == false) && (dbErr != nil) && (config.IsDatabaseConfigured() == true)

	if isUnavailable == true {
		result = dbErr
	}

	return result
}

// pushBotProfile renders the profile from the current settings and pushes
// the parts Telegram does not show yet.
func pushBotProfile(known map[string]string) (map[string]string, []string, error) {
	profile := services.RenderBotProfile()
	pushed, called, err := services.PushBotProfile(known, profile)

	return pushed, called, err
}
