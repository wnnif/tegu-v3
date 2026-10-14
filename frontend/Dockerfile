FROM node:24-alpine AS build
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
COPY VERSION /src/VERSION
RUN npm run build
FROM nginx:stable-alpine
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/frontend/dist /usr/share/nginx/html
EXPOSE 80
