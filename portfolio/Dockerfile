FROM nginx:1.27-alpine
RUN echo 'server { \
    listen 8080; \
    root /usr/share/nginx/html; \
    index index.html; \
    port_in_redirect off; \
    absolute_redirect off; \
    location / { try_files $uri $uri/ /index.html; } \
    gzip on; \
    gzip_types text/plain text/css application/json application/javascript text/xml image/svg+xml; \
}' > /etc/nginx/conf.d/default.conf
RUN sed -i 's|/run/nginx.pid|/tmp/nginx.pid|' /etc/nginx/nginx.conf \
    && mkdir -p /var/cache/nginx/client_temp /var/cache/nginx/proxy_temp \
               /var/cache/nginx/fastcgi_temp /var/cache/nginx/uwsgi_temp \
               /var/cache/nginx/scgi_temp \
    && chown -R 101:101 /var/cache/nginx /tmp
COPY src/ /usr/share/nginx/html/
EXPOSE 8080
USER 101
