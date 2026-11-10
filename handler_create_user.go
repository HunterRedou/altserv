package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
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
		return err
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create a User", err)
		return
	}

	respondWithJSON(w, http.StatusCreated, response{
		User: User{
			ID:        user.ID,
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
			Name:      user.Name,
			Role:      user.Role,
			Email:     user.Email,
			TenantID:  user.TenantID,
		},
	})
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
