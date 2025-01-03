FROM 1.26-alpine3.20-perl

RUN apk add --no-cache openssl

COPY default.conf /etc/nginx/nginx.conf.template
COPY start.sh /start.sh

RUN chmod +x /start.sh

ARG SERVER_NAME

ENV SERVER_NAME=${SERVER_NAME}

CMD ["/start.sh"]
