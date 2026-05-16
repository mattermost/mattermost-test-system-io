package oauth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User is the persisted user row for human sign-in.
type User struct {
	ID          uuid.UUID
	GitHubID    int64
	GitHubLogin string
	Email       *string
	DisplayName *string
	AvatarURL   *string
	Role        string
}

// Upsert persists (or updates) a user row keyed by github_id.
func Upsert(ctx context.Context, pool *pgxpool.Pool, gu GitHubUser) (User, error) {
	const q = `
		INSERT INTO users (github_id, github_login, email, display_name, avatar_url)
		VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''), NULLIF($5,''))
		ON CONFLICT (github_id) DO UPDATE
		  SET github_login = EXCLUDED.github_login,
		      email = EXCLUDED.email,
		      display_name = EXCLUDED.display_name,
		      avatar_url = EXCLUDED.avatar_url,
		      last_login_at = now()
		RETURNING id, github_id, github_login, email, display_name, avatar_url, role
	`
	var u User
	if err := pool.QueryRow(ctx, q, gu.ID, gu.Login, gu.Email, gu.Name, gu.AvatarURL).
		Scan(&u.ID, &u.GitHubID, &u.GitHubLogin, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Role); err != nil {
		return User{}, fmt.Errorf("upsert user: %w", err)
	}
	return u, nil
}
