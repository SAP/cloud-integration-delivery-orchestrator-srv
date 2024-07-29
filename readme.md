# maco-deploy

## Progress
- [x] tms backend
- [x] cpi backend
- [x] config api
- [ ] job api
- [ ] stage api
- [ ] execution api
- [ ] user/group api
- [ ] encrypt password
- [ ] oauth integration
- [ ] data checking before stored in db

## Local Development
1. prerequisites
    - make sure golang and make are installed
    - make sure to config env  `GOPROXY` to an accessible site, such as `https://goproxy.cn`
    - make sure docker engine and docker-compose are installed
2. start postgresql locally with [docker-compose-db.yml](./docker-compose-db.yml)
  ```
  docker compose -f docker-compose-db.yml up -d
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
  - /api/v1/config?id=xxxx, method `GET`, return cpi config with id `id`
  - /api/v1/config?type=xxx, method `GET`, return cpi config with type `type`,can be cpi or tms
  - /api/v1/config, method `POST` with data like [cpi-config.json](testData/cpi-create-config.json)
  - /api/v1/config, method `POST`, update the config like [cpi-update-config.json](./testData/cpi-update-config.json)
  - /api/v1/config?id=xxx, method `DELETE`, will delete the config, return the deleted config id
## URL

- https://permify.co/post/implement-oauth-2-golang-app/
- https://developer.okta.com/blog/2021/02/17/building-and-securing-a-go-and-gin-web-application
- https://github.com/markbates/goth
- https://github.com/crewjam/saml
- https://github.com/russellhaering/gosaml2
- github.com/appleboy/gin-jwt/v2
