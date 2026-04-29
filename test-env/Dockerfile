# tapper-sandbox
#
# Ubuntu 24.04-based development sandbox for the tapper project. Bundles the Go
# toolchain, common dev tooling, the user's `dots` bootstrapper, and a
# pre-provisioned `jlrickert` user with passwordless sudo. Layers are ordered
# slow-to-fast so the expensive apt/go/dots steps cache across rebuilds.
#
# Intended for local development only -- no sha256 pinning, root-in-group sudo,
# and the user's dotfiles are cloned unauthenticated over HTTPS.

FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ARG TARGETARCH

# Base system packages. Kept in a single apt layer so `rm -rf
# /var/lib/apt/lists/*` actually shrinks the image instead of leaving cache
# files behind in a lower layer.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        build-essential \
        pkg-config \
        zsh \
        sudo \
        locales \
        tzdata \
        ripgrep \
        fd-find \
        jq \
        less \
        openssh-client \
        vim-tiny \
        wget \
        unzip \
        software-properties-common \
    && rm -rf /var/lib/apt/lists/*

# Generate and default to a UTF-8 locale so Go tooling and the shell behave
# predictably regardless of the host's locale configuration.
RUN localedef -i en_US -c -f UTF-8 -A /usr/share/locale/locale.alias en_US.UTF-8
ENV LANG=en_US.UTF-8

# Install the Go toolchain from the official tarball. We branch on TARGETARCH
# so the same Dockerfile builds cleanly on both amd64 and arm64 hosts.
ARG GO_VERSION=1.26.0
RUN case "${TARGETARCH:-amd64}" in \
        amd64)  GO_ARCH=amd64  ;; \
        arm64)  GO_ARCH=arm64  ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}"; exit 1 ;; \
    esac && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" -o /tmp/go.tgz && \
    tar -C /usr/local -xzf /tmp/go.tgz && \
    rm /tmp/go.tgz
ENV PATH="/usr/local/go/bin:${PATH}"

# Create the unprivileged sandbox user with passwordless sudo. UID/GID default
# to 1000 to match the typical host user so bind-mount ownership lines up.
# Ubuntu 24.04 ships with a default `ubuntu` user at UID 1000, so we remove
# it first to free the slot.
ARG APP_USER=jlrickert
ARG APP_GROUP=jlrickert
ARG APP_UID=1000
ARG APP_GID=1000
RUN if id ubuntu >/dev/null 2>&1; then \
        userdel --remove ubuntu 2>/dev/null || userdel ubuntu; \
    fi && \
    groupadd --gid ${APP_GID} ${APP_GROUP} && \
    useradd --uid ${APP_UID} --gid ${APP_GID} --create-home --shell /bin/zsh ${APP_USER} && \
    echo "${APP_USER} ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/${APP_USER}-nopasswd && \
    chmod 0440 /etc/sudoers.d/${APP_USER}-nopasswd

USER jlrickert
WORKDIR /home/jlrickert

# Point Go at user-writable caches so `go install` works without sudo and so
# the volumes declared in docker-compose.yml attach to sensible targets.
ENV GOPATH=/home/jlrickert/go \
    GOCACHE=/home/jlrickert/.cache/go-build \
    PATH="/home/jlrickert/go/bin:${PATH}"

# Install the user's personal dots bootstrapper, then use it to materialize
# their public dotfiles into the home directory. Runs as jlrickert so the
# resulting files are owned correctly. Pinned to a specific commit so layer
# caching is deterministic; bump DOTS_REV when you want a newer dots.
ARG DOTS_REV=81f31990bbae1bc5e0dddf337f351feab43bbf87
RUN go install "github.com/jlrickert/dots/cmd/dots@${DOTS_REV}"

# DOTFILES_REV pins the dotfiles checkout. dots init has no --ref flag, so
# embedding the SHA in the layer command is what makes Docker invalidate
# and re-clone when this changes. Bump manually here, or use
# `task sandbox:refresh-dotfiles` to rebuild against the live HEAD.
ARG DOTFILES_REV=eef53375d9aaac923c1aded0bfcd648bf9d24558
RUN echo "dotfiles@${DOTFILES_REV}" \
    && dots init --from https://github.com/jlrickert/dotfiles.git \
                 --path dots-config \
                 --name jlrickert

# Install personal dotfile packages (common-shell pulls in transitively as a
# requires of zsh). SHELL=/bin/bash works around a dots bug where post_install
# hooks are invoked under POSIX sh; the zsh package's hook needs bash. Drop
# the prefix once dots is updated. dots install takes one package per call.
RUN SHELL=/bin/bash dots install jlrickert/zsh \
 && SHELL=/bin/bash dots install jlrickert/zellij

# Pre-create directories that the docker-compose volumes will mount over, so
# the first boot does not race on directory creation with the bind mounts.
RUN mkdir -p /home/jlrickert/go \
             /home/jlrickert/.cache/go-build \
             /home/jlrickert/.local/state/tapper

# zsh's default fpath includes /usr/local/share/zsh/site-functions; chown it
# to jlrickert so entrypoint.sh can drop Cobra-generated _tap / _keg files
# there at first boot without escalating. This keeps the dotfiles' zshrc out
# of the completion-wiring loop entirely.
USER root
RUN chown jlrickert:jlrickert /usr/local/share/zsh/site-functions
USER jlrickert

# Install the entrypoint. We briefly flip to root to COPY into /usr/local/bin
# and set the executable bit, then restore the unprivileged user so the
# container runtime defaults to jlrickert.
USER root
COPY --chown=jlrickert:jlrickert entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
USER jlrickert

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["sleep", "infinity"]
