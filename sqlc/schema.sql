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
  "config_name" varchar not NULL,
  "auth_url" varchar not NULL,
  "api_url" varchar not NULL,
  "auth_client_id" varchar not NULL,
  "auth_client_secret" varchar not NULL
);
