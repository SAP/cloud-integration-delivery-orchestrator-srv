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
SELECT * FROM api_endpoint
WHERE id = $1;

-- name: GetApiEndpointsByType :many
SELECT * FROM api_endpoint
WHERE type = $1;

-- name: GetApiEndpointsAll :many
SELECT * FROM api_endpoint;

-- name: DeleteApiEndpointById :one
DELETE FROM api_endpoint
WHERE id = $1 RETURNING *;

-- name: UpdateApiEndpointById :one
UPDATE  api_endpoint
SET name = $2, type=$3, description=$4, "authUrl"=$5, "apiUrl"=$6, "clientId"=$7, "clientSecret"=$8, status=$9
WHERE id = $1  RETURNING *;

-- name: CreateApiendpoint :one
INSERT INTO api_endpoint (
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
  description,
  status
) VALUES (
  $1, $2, $3
) RETURNING *;

-- name: GetJobByID :one
SELECT * FROM job
WHERE id = $1 LIMIT 1;

-- name: GetJobs :many
SELECT * FROM job;

-- name: UpdateJobByID :one
UPDATE job
SET name=$2, description=$3, status=$4
WHERE id=$1 RETURNING *;

-- name: DeleteJobByID :one
DELETE FROM job
WHERE id = $1 RETURNING *;

-- name: CreateStep :one
INSERT INTO step (
    job_id,
    name,
    templ_type,
    status
) VALUES (
    $1, $2, $3, $4
) RETURNING *;

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

-- name: UpdateTMStmplByID :one
UPDATE tmstmpl
SET step_id=$2, tms_config_id=$3, tms_node_id=$4, tms_tr_ids=$5, status=$6
WHERE id=$1 RETURNING *;


-- name: DeleteTMStmplByID :one
DELETE FROM tmstmpl
WHERE id = $1  RETURNING *;

-- name: DeleteTMStmplByStepID :one
DELETE FROM tmstmpl
WHERE step_id = $1  RETURNING *;


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

-- name: GetCPItmplByStepID :many
SELECT * FROM cpitmpl
WHERE step_id = $1;

-- name: UpdateCPItmplByID :one
UPDATE cpitmpl
SET step_id=$2, cpi_config_id=$3, cpi_package_ids=$4, cpi_iflow_ids=$5, cpi_script_ids=$6, status=$7
WHERE id=$1 RETURNING *;

-- name: DeleteCPItmpl :one
DELETE FROM cpitmpl
WHERE id = $1  RETURNING *;

-- name: DeleteCPItmplByStepID :one
DELETE FROM cpitmpl
WHERE id = $1  RETURNING *;

-- name: CreateCpiArtifact :one
INSERT  INTO cpiartifacts (
cpi_tmpl_id,
cpi_item_id,
cpi_item_version
) VALUES (
$1, $2, $3
)RETURNING *;

-- name: GetCpiArtifactsByCpiTmplID :many
SELECT * FROM cpiartifacts
WHERE cpi_tmpl_id = $1;

-- name: GetCpiArtifactByID :one
SELECT * FROM cpiartifacts
WHERE id = $1 LIMIT 1;

-- name: UpdateCpiArtifact :one
UPDATE cpiartifacts
SET cpi_tmpl_id = $2,  cpi_item_id = $3, cpi_item_version = $4
WHERE id = $1 RETURNING *;

-- name: DeleteCpiArtifactByID :one
DELETE FROM cpiartifacts
WHERE id = $1 RETURNING *;

-- name: DeleteCpiArtifactByCpiTmplID :one
DELETE FROM cpiartifacts
WHERE cpi_tmpl_id = $1 RETURNING *;


-- name: InsertImportStep :one
INSERT INTO import_step (
  job_id,
  status,
  sequence,
  endpoint_id,
  transport_node_id,
  transport_node_name,
  transport_requests
) VALUES (
  $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: InsertDeployStep :one
INSERT INTO deploy_step (
  job_id,
  status,
  sequence,
  endpoint_id,
  endpoint_name,
  package_id,
  artifact_ids
) VALUES (
  $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: SelectImportStepsByJobId :many
SELECT * From import_step WHERE job_id=$1;

-- name: SelectDeployStepsByJobId :many
SELECT * FROM deploy_step WHERE job_id=$1;


-- name: UpdateImportStep :one
UPDATE import_step
SET status=$2, endpoint_id=$3, transport_node_id=$4, transport_node_name=$5, transport_requests=$6, sequence=$7
WHERE id=$1 RETURNING *;


-- name: UpdateDeployStep :one
UPDATE deploy_step
SET status=$2, endpoint_id=$3, sequence=$4, endpoint_name=$5, package_id=$6, artifact_ids=$7
WHERE id=$1 RETURNING *;


-- name: DeleteImportStepByJobId :exec
DELETE FROM import_step
WHERE job_id=$1;

-- name: DeleteDeployStepByJobId :exec
DELETE FROM deploy_step
WHERE job_id=$1;

-- name: DeleteImportStepById :exec
DELETE FROM import_step
WHERE id=$1;

-- name: DeleteDeployStepById :exec
DELETE FROM deploy_step
WHERE id=$1;