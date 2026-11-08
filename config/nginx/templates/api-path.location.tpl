
    # API on the same domain under __API_PATH__ (the prefix is forwarded to the backend unchanged)
    location __API_PATH__/ {
        client_max_body_size 20m;
        limit_req zone=api_perip burst=60 nodelay;
        limit_conn conn_perip 50;
        set $upstream_api __API_UPSTREAM__;
        proxy_pass $upstream_api;
        include /etc/nginx/snippets/proxy.conf;
    }
