CREATE TABLE "users" (
  "id" SERIAL PRIMARY KEY,
  "username" varchar NOT NULL,
  "hashed_password" varchar NOT NULL,
  "full_name" varchar NOT NULL,
  "email" varchar UNIQUE NOT NULL,
  "password_changed_at" timestamptz NOT NULL DEFAULT('0001-01-01 00:00:00Z'),
  "created_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "groups" (
  "id" SERIAL PRIMARY KEY,
  "group_name" varchar NOT NULL
);


CREATE TABLE IF NOT EXISTS "api_endpoint" (
  "id" SERIAL PRIMARY KEY,
  "name" varchar not NULL,
  "description" varchar,
  "type" varchar not NULL,
  "authUrl" varchar not NULL,
  "apiUrl" varchar not NULL,
  "clientId" varchar not NULL,
  "clientSecret" varchar not NULL,
  "status" varchar not NULL,
  "modifiedAt" varchar not NULL,
  "modifiedBy" varchar not NULL,
  "createdAt" varchar not NULL,
  "createdBy" varchar not NULL
);

CREATE TABLE "job" (
  "id" SERIAL PRIMARY KEY,
  "name" varchar,
  "description" varchar,
  "status" varchar
);

CREATE TABLE "step" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "name" varchar not NULL,
  "templ_type" varchar not NULL,
  "templ_id" integer,
  "status" varchar
);

CREATE TABLE "tmstmpl" (
    "id" SERIAL PRIMARY KEY,
    "step_id" integer not NULL,
    "tms_config_id" integer not NULL,
    "tms_node_id" integer not NULL,
    "tms_tr_ids" integer[] not null,
    "status" varchar
);

CREATE TABLE "cpitmpl" (
      "id" SERIAL PRIMARY KEY,
      "step_id" integer not NULL,
      "cpi_config_id" integer not NULL,
      "cpi_package_ids" varchar[] not NULL,
      "cpi_iflow_ids" varchar[] not NULL,
      "cpi_script_ids" varchar[] not NULL,
      "status" varchar
);

CREATE TABLE IF NOT EXISTS "cpiartifacts" (
    "id" SERIAL PRIMARY KEY,
    "cpi_tmpl_id" integer not NULL,
    "cpi_item_id" varchar not NULL,
    "cpi_item_version" real not NULL
);

CREATE TABLE "execution" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "status" varchar
);

CREATE TABLE IF NOT EXISTS "import_step" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "sequence" integer not NULL,
  "status" varchar not NULL,
  "endpoint_id" integer not NULL,
  "transport_node_id" integer not NULL,
  "transport_node_name" varchar not NULL,
  "transport_requests" integer[],

  "created_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE IF NOT EXISTS "deploy_step" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "sequence" integer not NULL,
  "status" varchar not NULL,
  "endpoint_id" integer not NULL, -- ApiEndpoint
  "package_id" integer not NULL,
  "artifact_ids" integer[],

  "created_at" timestamptz NOT NULL DEFAULT (now())
);
