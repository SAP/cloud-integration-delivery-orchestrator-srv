CREATE TABLE "execution_log" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "timestamp" timestamptz,
  "log" varchar
);

CREATE TABLE "job" (
  "id" SERIAL PRIMARY KEY,
  "name" varchar,
  "type" varchar, -- deploy undeploy import
  "description" varchar,
  "status" varchar,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "created_by" varchar,
  "modified_at" timestamptz NOT NULL DEFAULT (now()),
  "modified_by" varchar
);

CREATE TABLE IF NOT EXISTS "import_step" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "sequence" integer not NULL, -- sequence of the step within a job
  "status" varchar not NULL,
  "transport_node_id" integer not NULL,
  "transport_node_name" varchar not NULL,
  "transport_requests" integer[]
);

CREATE TABLE IF NOT EXISTS "deploy_step" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "sequence" integer not NULL,
  "status" varchar not NULL,
  "endpoint" varchar not NULL, -- i.e. CPI tenant name, also a destination name
  "package_id" varchar not NULL,
  "artifact_ids" varchar[]
);
