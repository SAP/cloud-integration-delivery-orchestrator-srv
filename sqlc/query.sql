-- name: CreateJob :one
INSERT INTO job (
  name,
  description,
  status,
  type,
  created_by
) VALUES (
  $1, $2, $3, $4, $5
) RETURNING *;

-- name: GetJobByID :one
SELECT * FROM job
WHERE id = $1 LIMIT 1;

-- name: GetJobs :many
SELECT * FROM job;

-- name: GetJobsByType :many
SELECT * FROM job WHERE type=$1;

-- name: UpdateJobByID :one
UPDATE job
SET 
  name=$2, 
  description=$3, 
  status=$4, 
  modified_at=$5, 
  modified_by=$6
WHERE id=$1 RETURNING *;

-- name: DeleteJobByID :one
DELETE FROM job
WHERE id = $1 RETURNING *;

-- name: InsertImportStep :one
INSERT INTO import_step (
  job_id,
  status,
  sequence,
  transport_node_id,
  transport_node_name,
  transport_requests
) VALUES (
  $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: InsertDeployStep :one
INSERT INTO deploy_step (
  job_id,
  status,
  sequence,
  endpoint,
  package_id,
  artifact_ids
) VALUES (
  $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: SelectImportStepsByJobId :many
SELECT * From import_step WHERE job_id=$1;

-- name: SelectDeployStepsByJobId :many
SELECT * FROM deploy_step WHERE job_id=$1;


-- name: UpdateImportStep :one
UPDATE import_step
SET status=$2, sequence=$3, transport_node_id=$4, transport_node_name=$5, transport_requests=$6
WHERE id=$1 RETURNING *;

-- name: UpdateActionId :one
UPDATE import_step
SET action_id=$2
WHERE id=$1 RETURNING *;


-- name: UpdateDeployStep :one
UPDATE deploy_step
SET status=$2, endpoint=$3, sequence=$4, package_id=$5, artifact_ids=$6
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


-- name: SelectArtifactStatusByJobJd :many
SELECT * FROM artifact_status
WHERE "jobId"=$1;

-- name: InsertArtifactStatus :one
INSERT INTO artifact_status(
  "jobId",
  step_id,
  artifact_type,
  artifact_id,
  task_id,
  status
) VALUES (
  $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: updateArtifactStatus :one
UPDATE artifact_status
SET task_id=$2, status=$3, artifact_type=$4, artifact_id=$5
WHERE id=$1
RETURNING *;