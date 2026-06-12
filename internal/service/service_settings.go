package service

import (
	"errors"

	"bethoven/internal/db"
	"bethoven/internal/models"
)

// settingPublicBets is the KV key for the "everyone sees everyone's picks"
// toggle. Stored as "1" (on) / "0" (off); absent means off.
const settingPublicBets = "public_bets"

// PublicBetsEnabled reports whether the admin has opened the all-bets grid to
// every player. Defaults to false when the setting has never been written.
func (s *Service) PublicBetsEnabled() (bool, error) {
	v, err := s.store.GetSetting(settingPublicBets)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v == "1", nil
}

// SetPublicBets toggles whether all players may see everyone's picks. Admin only.
func (s *Service) SetPublicBets(by *models.User, enabled bool) error {
	if err := requireAdmin(by); err != nil {
		return err
	}
	v := "0"
	if enabled {
		v = "1"
	}
	return s.store.SetSetting(settingPublicBets, v)
}
