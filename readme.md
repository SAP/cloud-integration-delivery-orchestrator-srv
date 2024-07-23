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

## Local Development
1. prerequisites
    - make sure golang and make are installed
    - make sure to config env  `GOPROXY` to an accessible site, such as `https://goproxy.cn`
    - make sure docker engine and docker-compose are installed
2. start postgresql locally with [docker-compose-db.yml](./docker-compose-db.yml)
3. connect to db, and create required tables from [schema.sql](./sqlc/schema.sql)
4. add sample data
4. start application locally, it will listen at `0.0.0.0:9000`
    ```
    make
    ```

## APIs
- config api
  - /api/v1/cpiconfigs, method `GET`, return all cpi configs
  - /api/v1/cpiconfig?name=xxxx, method `GET`, return cpi config with name `xxxx`
  - /api/v1/cpiconfig, method `POST` with data like [cpi-config.json](testData/cpi-config.json)
## URL

- https://permify.co/post/implement-oauth-2-golang-app/
- https://developer.okta.com/blog/2021/02/17/building-and-securing-a-go-and-gin-web-application
- https://github.com/markbates/goth
- https://github.com/crewjam/saml
- https://github.com/russellhaering/gosaml2
- github.com/appleboy/gin-jwt/v2
