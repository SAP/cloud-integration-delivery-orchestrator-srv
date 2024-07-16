-- name: CreateUser :one
INSERT INTO users (
  username,
  hashed_password,
  full_name,
  email
) VALUES (
  $1, $2, $3, $4
) RETURNING *;

-- name: GetUser :one
SELECT * FROM users
WHERE username = $1 LIMIT 1;

-- name: GetUsers :many
SELECT * FROM users
ORDER BY username;

-- name: CreateGroup :one
INSERT INTO groups (
  group_name
) VALUES (
  $1
) RETURNING *;

-- name: GetGroup :one
SELECT * FROM groups
WHERE group_name = $1 LIMIT 1;

-- name: GetGroups :many
SELECT * FROM groups
ORDER BY group_name;

-- name: CreateConfig :one
INSERT INTO config (
  config_name,
  auth_url,
  api_url,
  auth_client_id,
  auth_client_secret
) VALUES (
  $1, $2, $3, $4, $5
) RETURNING *;

-- name: GetConfig :one
SELECT * FROM config
WHERE config_name = $1 LIMIT 1;
