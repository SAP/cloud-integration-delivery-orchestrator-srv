## Local Development
1. prerequisites
    - make sure golang and make are installed
    - make sure to config env  `GOPROXY` to an accessible site, such as `https://goproxy.cn`
    - make sure docker engine and docker-compose or podman-desktop are installed
2. start postgresql locally with [docker-compose-db.yml](./docker-compose-db.yml)
  ```
  docker compose -f docker-compose-db.yml up -d
  ```
  ```
  podman compose -f docker-compose-db.yml up -d
  ```
3. connect to db, and create required tables from [schema.sql](./sqlc/schema.sql)
4. add sample data
4. start application locally, it will listen at `0.0.0.0:9000`
    ```
    make
    ```

## APIs
- config api
  - /api/v1/config, method `GET`, return all cpi configs
  - /api/v1/config, method `POST`, create config using data like [cpi-config.json](testData/cpi-create-config.json)
  - /api/v1/config/id, method `GET`, return  config with id `id`
  - /api/v1/config/id, method `PUT`, update the config  with `id` using data like [cpi-update-config.json](./testData/cpi-update-config.json)
  - /api/v1/config/id, method `DELETE`, will delete the config `id`, return the deleted config id
- job api
  - /api/v1/job, method `GET`, get all jobs
  - /api/v1/job, method `POST`, create job using data like [create-job.json](testData/create-job.json)
  - /api/v1/job/:id, method `GET`, get job with `id`
  - /api/v1/job/:id, method `PUT`, update job using data like  [update.json](testData/update-job.json)
  - /api/v1/job/:id, method `DELETE`, delete job with `id¸`

## URL

- https://permify.co/post/implement-oauth-2-golang-app/
- https://developer.okta.com/blog/2021/02/17/building-and-securing-a-go-and-gin-web-application
- https://github.com/markbates/goth
- https://github.com/crewjam/saml
- https://github.com/russellhaering/gosaml2
- github.com/appleboy/gin-jwt/v2


## default-env.json
provide environment variables: VCAP_SERVICES. need three service bindings:
- postgresql-db
- destination
- transport
put the file in root directory of the repo

## TODO List
Frontend:
[x] status check display in step Component(success, failed, running).
[x] datatable should support search.

Backend:
- maco400 authentications
- optimize job execution log display
- undeploy artifacts
- cache for oauth tokens
- ppms and blackduck change request(further feature)


## user provided environment variables
PORT = 9000

## Deploy to CF
```sh
cf login -a https://api.cf.sap.hana.ondemand.com/ -o MaCo-devops -s DEVOPS
cf push
```

## connect to remote DB locally
Since pgsql cannot directly be connected locally, can only connect via cf runtime.
So firstly run cf application that binds pgsql service instance, then use this command to start a proxy via cf app runtime:
```
cf ssh -L localhost:8866:postgres-d8fb591a-f9bb-4cfc-9314-4e1dda274f27.cxxzc36no8yr.eu-central-1.rds.amazonaws.com:8828 mmt.devops.srv.cpi.delivery -N
```