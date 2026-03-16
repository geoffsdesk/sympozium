# Multi-stage build: Astro static site → nginx
FROM node:20-slim AS build
WORKDIR /app
COPY package.json ./
RUN npm install
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
# Custom nginx config for non-root, port 8080, serving under /sympozium subpath
RUN echo 'server { \
    listen 8080; \
    root /usr/share/nginx/html; \
    index index.html; \
    port_in_redirect off; \
    absolute_redirect off; \
    location /sympozium { \
        rewrite ^/sympozium/?(.*)$ /$1 break; \
        try_files $uri $uri/index.html /index.html; \
    } \
    gzip on; \
    gzip_types text/plain text/css application/json application/javascript text/xml; \
}' > /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
USER 101
