FROM node:22-alpine
ENV HOST=0.0.0.0 NODE_ENV=production PORT=3000
WORKDIR /app
COPY --chown=node:node .output ./.output
USER node
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
