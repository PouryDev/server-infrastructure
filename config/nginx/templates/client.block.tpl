server {
    listen 80;
    server_name __CLIENT_DOMAIN__;
    set $upstream_client __CLIENT_UPSTREAM__;
__API_PATH_LOCATION__
    location / {
        proxy_pass $upstream_client;
        include /etc/nginx/snippets/proxy.conf;
    }
}
