-- name: UpsertUserByGitHubID :one
INSERT INTO users (github_id, github_login, email, display_name, avatar_url)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (github_id) DO UPDATE
    SET github_login = EXCLUDED.github_login,
        email = EXCLUDED.email,
        display_name = EXCLUDED.display_name,
        avatar_url = EXCLUDED.avatar_url,
        last_login_at = now()
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 LIMIT 1;

-- name: UpdateUserRole :exec
UPDATE users SET role = $2 WHERE id = $1;
