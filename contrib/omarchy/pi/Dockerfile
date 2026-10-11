# pi-box: Pi in a container that only sees the project folder (contrib/omarchy/pi).
FROM node:24-bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash ca-certificates git ripgrep python3 curl \
 && rm -rf /var/lib/apt/lists/* \
 && curl -LsSf https://astral.sh/uv/0.9.5/install.sh | env UV_INSTALL_DIR=/usr/local/bin sh
RUN npm install -g --ignore-scripts @earendil-works/pi-coding-agent@1.1.0
ENV HOME=/home/pi PI_OFFLINE=0
WORKDIR /workspace
ENTRYPOINT ["pi"]
