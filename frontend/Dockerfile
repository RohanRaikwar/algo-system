# Frontend image: builds the Vite bundle and serves it as a static SPA.
FROM node:20-bookworm-slim AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
# Public URL of the api_gateway, baked in at build time
# (e.g. https://backend-production.up.railway.app). Empty = same origin.
ARG VITE_API_URL=""
ENV VITE_API_URL=${VITE_API_URL}
RUN npm run build

FROM node:20-bookworm-slim
RUN npm install -g serve@14
WORKDIR /app
COPY --from=build /src/dist ./dist
ENV PORT=3000
EXPOSE 3000
CMD ["sh", "-c", "serve -s dist -l tcp://0.0.0.0:${PORT}"]
