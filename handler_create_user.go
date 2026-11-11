package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

var (
	errUserNotFound   = errors.New("user not found")
	errRoleNotAllowed = errors.New("role change not allowed")
	errLastChef       = errors.New("cannot remove the last chef")
)

type User struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Password  string    `json:"-"`
	Email     string    `json:"email"`
	TenantID  uuid.UUID `json:"tenant_id"`
}

// validRole keeps the API and the database CHECK constraint in sync.
func validRole(role string) bool {
	switch role {
	case "platform_admin", "chef", "employee":
		return true
	}
	return false
}

// canManageUsers reports whether a role may list and create users.
// Employees only see their assigned patients (doc 4).
func canManageUsers(role string) bool {
	return role == "chef" || role == "platform_admin"
}

// canAssignRole prevents a chef from escalating an employee to platform admin.
// A platform admin may create every role.
func canAssignRole(actorRole, targetRole string) bool {
	switch actorRole {
	case "platform_admin":
		return true
	case "chef":
		return targetRole == "chef" || targetRole == "employee"
	}
	return false
}

// canChangeRole checks both the current and the new role. A chef may only move
// a user between chef and employee. A platform admin may assign any role. An
// employee can change nothing.
func canChangeRole(actorRole, currentRole, newRole string) bool {
	switch actorRole {
	case "platform_admin":
		return true
	case "chef":
		return (currentRole == "chef" || currentRole == "employee") &&
			(newRole == "chef" || newRole == "employee")
	}
	return false
}

func toUser(user db.User) User {
	return User{
		ID:        user.ID,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
		Name:      user.Name,
		Role:      user.Role,
		Email:     user.Email,
		TenantID:  user.TenantID,
	}
}

func (cfg *apiConfig) handlerUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	if !canManageUsers(actor.Role) {
		respondWithError(w, http.StatusForbidden, "Not allowed to create users", nil)
		return
	}

	type parameters struct {
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	type response struct {
		User
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	if !validRole(params.Role) {
		respondWithError(w, http.StatusBadRequest, "Invalid role", nil)
		return
	}
	if !canAssignRole(actor.Role, params.Role) {
		respondWithError(w, http.StatusForbidden, "Not allowed to assign this role", nil)
		return
	}

	hashedPassword, err := auth.HashPassword(params.Password)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't hash Password", err)
		return
	}

	// The tenant always comes from the session, never from the request body.
	var user db.User
	err = cfg.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		user, err = q.CreateUser(r.Context(), db.CreateUserParams{
			Name:           params.Name,
			Role:           params.Role,
			HashedPassword: hashedPassword,
			Email:          params.Email,
			TenantID:       actor.TenantID,
		})
		if err != nil {
			return err
		}
		return cfg.recordAudit(r.Context(), q, audit.Event{
			TenantID:    actor.TenantID,
			ActorUserID: actor.ID,
			Action:      "user.created",
			EntityType:  "user",
			EntityID:    user.ID,
			RequestID:   requestIDFromContext(r.Context()),
			Metadata:    map[string]any{"role": user.Role},
		})
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create a User", err)
		return
	}

	respondWithJSON(w, http.StatusCreated, response{User: toUser(user)})
}

// handlerUpdateUserRole handles PATCH /api/users/{id}. A chef may change a user
// between chef and employee, but not to or from platform_admin. A platform
// admin may assign any role. Nobody may change their own role, and the last
// chef of a tenant cannot be demoted. Every change is audited.
func (cfg *apiConfig) handlerUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	if !canManageUsers(actor.Role) {
		respondWithError(w, http.StatusForbidden, "Not allowed to change roles", nil)
		return
	}

	targetID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user id", nil)
		return
	}
	if targetID == actor.ID {
		respondWithError(w, http.StatusForbidden, "You cannot change your own role", nil)
		return
	}

	type parameters struct {
		Role string `json:"role"`
	}
	params := parameters{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	if !validRole(params.Role) {
		respondWithError(w, http.StatusBadRequest, "Invalid role", nil)
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

		if !canChangeRole(actor.Role, current.Role, params.Role) {
			return errRoleNotAllowed
		}
		if current.Role == params.Role {
			updated = current
			return nil
		}

		// Demoting the last chef would leave the tenant without an
		// administrator.
		if current.Role == "chef" {
			chefs, err := q.CountChefsByTenant(r.Context(), actor.TenantID)
			if err != nil {
				return err
			}
			if chefs <= 1 {
				return errLastChef
			}
		}

		updated, err = q.UpdateUserRole(r.Context(), db.UpdateUserRoleParams{
			ID:       targetID,
			TenantID: actor.TenantID,
			Role:     params.Role,
		})
		if err != nil {
			return err
		}

		// The old session carries the old role. Revoking it forces a fresh
		// login, and a promotion to a privileged role also triggers MFA
		// enrollment enforcement.
		if err := q.RevokeAllUserSessions(r.Context(), db.RevokeAllUserSessionsParams{
			UserID:   targetID,
			TenantID: actor.TenantID,
		}); err != nil {
			return err
		}

		return cfg.recordAudit(r.Context(), q, audit.Event{
			TenantID:    actor.TenantID,
			ActorUserID: actor.ID,
			Action:      "user.role_changed",
			EntityType:  "user",
			EntityID:    updated.ID,
			RequestID:   requestIDFromContext(r.Context()),
			Metadata: map[string]any{
				"old_role": current.Role,
				"new_role": updated.Role,
			},
		})
	})
	switch {
	case errors.Is(err, errUserNotFound):
		respondWithError(w, http.StatusNotFound, "User not found", nil)
		return
	case errors.Is(err, errRoleNotAllowed):
		respondWithError(w, http.StatusForbidden, "Not allowed to assign this role", nil)
		return
	case errors.Is(err, errLastChef):
		respondWithError(w, http.StatusConflict, "Cannot remove the last chef", nil)
		return
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "Couldn't change the role", err)
		return
	}

	respondWithJSON(w, http.StatusOK, toUser(updated))
}

func (cfg *apiConfig) handlerGetUsers(w http.ResponseWriter, r *http.Request) {
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	if !canManageUsers(actor.Role) {
		respondWithError(w, http.StatusForbidden, "Not allowed to list users", nil)
		return
	}

	users := []User{}
	err := cfg.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		dbUsers, err := q.ListUsersByTenant(r.Context(), actor.TenantID)
		if err != nil {
			return err
		}
		for _, dbUser := range dbUsers {
			users = append(users, User{
				ID:        dbUser.ID,
				CreatedAt: dbUser.CreatedAt,
				UpdatedAt: dbUser.UpdatedAt,
				Name:      dbUser.Name,
				Role:      dbUser.Role,
				Email:     dbUser.Email,
				TenantID:  dbUser.TenantID,
			})
		}
		return nil
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't list users", err)
		return
	}
	respondWithJSON(w, http.StatusOK, users)
}
