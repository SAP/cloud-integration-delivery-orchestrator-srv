CREATE TABLE IF NOT EXISTS "deploy_step" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "sequence" integer not NULL,
  "status" varchar not NULL,
  "endpoint_id" integer not NULL, -- ApiEndpoint
  "endpoint_name" varchar not NULL, -- i.e. CPI tenants name
  "package_id" varchar not NULL,
  "artifact_ids" varchar[],

  "created_at" timestamptz NOT NULL DEFAULT (now())
);