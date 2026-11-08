server {
    listen 80;
    server_name __API_DOMAIN__;
    client_max_body_size 20m;
    set $upstream_api __API_UPSTREAM__;

    location / {
        limit_req zone=api_perip burst=60 nodelay;
        limit_conn conn_perip 50;
        proxy_pass $upstream_api;
        include /etc/nginx/snippets/proxy.conf;
    }
}
