# Production stage - más simple como tu compañero
FROM nginx:alpine

# Copiar build al servidor nginx
COPY build/ /usr/share/nginx/html

# Copiar configuración de nginx
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
