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


CREATE TABLE "config" (
  "id" SERIAL PRIMARY KEY,
  "name" varchar not NULL,
  "type" varchar not NULL,
  "auth_url" varchar not NULL,
  "api_url" varchar not NULL,
  "auth_client_id" varchar not NULL,
  "auth_client_secret" varchar not NULL
);

CREATE TABLE "job" (
  "id" SERIAL PRIMARY KEY,
  "name" varchar not NULL,
  "steps" integer[] not NULL,
  "status" varchar not NULL
);

CREATE TABLE "step" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "name" varchar not NULL,
  "templ_type" varchar not NULL,
  "templ_id" integer not NULL,
  "status" varchar not NULL
);

CREATE TABLE "tmstmpl" (
    "id" SERIAL PRIMARY KEY,
    "step_id" integer not NULL,
    "tms_config_id" integer not NULL,
    "tms_node_id" integer not NULL,
    "tms_tr_ids" integer[] not null,
    "status" varchar not NULL
);

CREATE TABLE "cpitmpl" (
      "id" SERIAL PRIMARY KEY,
      "step_id" integer not NULL,
      "cpi_config_id" integer not NULL,
      "cpi_package_ids" varchar[] not NULL,
      "cpi_iflow_ids" varchar[] not NULL,
      "cpi_script_ids" varchar[] not NULL,
      "status" varchar not NULL
);

CREATE TABLE "execution" (
  "id" SERIAL PRIMARY KEY,
  "job_id" integer not NULL,
  "status" varchar not NULL,
  "logs" json
);
