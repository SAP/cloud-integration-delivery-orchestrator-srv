CREATE TABLE IF NOT EXISTS  "import_step" (
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