package main

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// handlerDisableUser locks an account and revokes every session. A chef cannot
// disable a platform admin, itself, or the last active chef. The change is
// audited with the role only, never with health data.
func (cfg *apiConfig) handlerDisableUser(w http.ResponseWriter, r *http.Request) {
	cfg.setUserDisabled(w, r, true)
}

// handlerEnableUser clears the disabled state. A disabled account has no
// sessions, so enabling does not restore any old session.
func (cfg *apiConfig) handlerEnableUser(w http.ResponseWriter, r *http.Request) {
	cfg.setUserDisabled(w, r, false)
}

func (cfg *apiConfig) setUserDisabled(w http.ResponseWriter, r *http.Request, disable bool) {
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	if !canManageUsers(actor.Role) {
		respondWithError(w, http.StatusForbidden, "Not allowed to change user state", nil)
		return
	}

	targetID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user id", nil)
		return
	}
	if targetID == actor.ID {
		respondWithError(w, http.StatusForbidden, "You cannot change your own account state", nil)
		return
	}

	var updated db.User
	err = cfg.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		current, err := q.GetUserByID(r.Context(), db.GetUserByIDParams{
			ID:       targetID,
			TenantID: actor.TenantID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return errUserNotFound
		}
		if err != nil {
			return err
		}
		if actor.Role == "chef" && current.Role == "platform_admin" {
			return errRoleNotAllowed
		}
		if disable && current.Role == "chef" && !current.DisabledAt.Valid {
			chefs, err := q.CountChefsByTenant(r.Context(), actor.TenantID)
			if err != nil {
				return err
			}
			if chefs <= 1 {
				return errLastChef
			}
		}

		if disable {
			updated, err = q.DisableUser(r.Context(), db.DisableUserParams{ID: targetID, TenantID: actor.TenantID})
		} else {
			updated, err = q.EnableUser(r.Context(), db.EnableUserParams{ID: targetID, TenantID: actor.TenantID})
		}
		if errors.Is(err, sql.ErrNoRows) {
			return errUserNotFound
		}
		if err != nil {
			return err
		}

		if disable {
			if err := q.RevokeAllUserSessions(r.Context(), db.RevokeAllUserSessionsParams{
				UserID:   targetID,
				TenantID: actor.TenantID,
			}); err != nil {
				return err
			}
		}

		action := "user.enabled"
		if disable {
			action = "user.disabled"
		}
		return cfg.recordAudit(r.Context(), q, audit.Event{
			TenantID:    actor.TenantID,
			ActorUserID: actor.ID,
			Action:      action,
			EntityType:  "user",
			EntityID:    updated.ID,
			RequestID:   requestIDFromContext(r.Context()),
			Metadata:    map[string]any{"role": updated.Role},
		})
	})
	switch {
	case errors.Is(err, errUserNotFound):
		respondWithError(w, http.StatusNotFound, "User not found", nil)
		return
	case errors.Is(err, errRoleNotAllowed):
		respondWithError(w, http.StatusForbidden, "Not allowed to change this account", nil)
		return
	case errors.Is(err, errLastChef):
		respondWithError(w, http.StatusConflict, "Cannot disable the last chef", nil)
		return
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "Couldn't change the account state", err)
		return
	}

	respondWithJSON(w, http.StatusOK, toUser(updated))
}
