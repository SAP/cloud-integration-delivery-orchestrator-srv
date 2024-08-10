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

-- name: GetApiEndpointById :one
SELECT * FROM "ApiEndpoint"
WHERE id = $1;

-- name: GetApiEndpointsByType :many
SELECT * FROM "ApiEndpoint"
WHERE type = $1;

-- name: GetApiEndpointsAll :many
SELECT * FROM "ApiEndpoint";

-- name: DeleteApiEndpointById :one
DELETE FROM "ApiEndpoint"
WHERE id = $1 RETURNING *;

-- name: UpdateApiEndpointById :one
UPDATE  "ApiEndpoint"
SET name = $2, type=$3, description=$4, "authUrl"=$5, "apiUrl"=$6, "clientId"=$7, "clientSecret"=$8, status=$9
WHERE id = $1  RETURNING *;

-- name: CreateApiendpoint :one
INSERT INTO "ApiEndpoint" (
  name,
  type,
  description,
  status,
  "authUrl",
  "apiUrl",
  "clientId",
  "clientSecret",
  "createdAt",
  "createdBy",
  "modifiedAt",
  "modifiedBy"
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
) RETURNING *;

-- name: CreateJob :one
INSERT INTO job (
  name,
  steps
) VALUES (
  $1, $2
) RETURNING *;

-- name: GetJobByID :one
SELECT * FROM job
WHERE id = $1 LIMIT 1;

-- name: GetJobs :many
SELECT * FROM job;

-- name: UpdateJobByID :one
UPDATE job
SET name=$2,steps = $3
WHERE id = $1 RETURNING *;

-- name: DeleteJobByID :one
DELETE FROM job
WHERE id = $1 RETURNING *;

-- name: CreateStep :one
INSERT  INTO step (
    job_id,
    name,
    templ_type,
    status
) VALUES (
    $1, $2, $3, $4
)RETURNING *;

-- name: GetStepByID :one
SELECT * FROM step
WHERE id =$1 LIMIT 1;

-- name: GetStepsByJobID :many
SELECT * FROM step
WHERE job_id =$1;

-- name: GetSteps :many
SELECT * FROM job;

-- name: UpdateStepByID :one
UPDATE step
SET job_id=$2, name=$3, templ_type=$3, templ_id=$4, status=$5
WHERE id = $1 RETURNING *;

-- name: DeleteStepByID :one
DELETE FROM step
WHERE id=$1 RETURNING *;


-- name: DeleteStepByJobID :many
DELETE FROM step
WHERE job_id=$1 RETURNING *;

-- name: CreateTMStmpl :one
INSERT  INTO tmstmpl (
step_id,
tms_config_id,
tms_node_id,
tms_tr_ids,
status
) VALUES (
$1, $2, $3, $4, $5
)RETURNING *;

-- name: GetTMStmplByID :one
SELECT * FROM tmstmpl
WHERE id =$1 LIMIT 1;

-- name: GetTMStmpl :many
SELECT * FROM tmstmpl;

-- name: UpdateTMStmpl :one
UPDATE tmstmpl
SET step_id=$2, tms_config_id=$3, tms_node_id=$4, tms_tr_ids=$5, status=$6
WHERE id=$1 RETURNING *;

-- name: DeleteTMStmpl :one
DELETE FROM tmstmpl
WHERE id = $1  RETURNING *;


-- name: CreateCPItmpl :one
INSERT  INTO cpitmpl (
step_id,
cpi_config_id,
cpi_package_ids,
cpi_iflow_ids,
cpi_script_ids,
status
) VALUES (
$1, $2, $3, $4, $5, $6
)RETURNING *;

-- name: GetCPItmplByID :one
SELECT * FROM cpitmpl
WHERE id =$1 LIMIT 1;

-- name: GetCPItmpl :many
SELECT * FROM cpitmpl;

-- name: UpdateCPItmpl :one
UPDATE cpitmpl
SET step_id=$2, cpi_config_id=$3, cpi_package_ids=$4, cpi_iflow_ids=$5, cpi_script_ids=$6, status=$7
WHERE id=$1 RETURNING *;

-- name: DeleteCPItmpl :one
DELETE FROM cpitmpl
WHERE id = $1  RETURNING *;
