#!/usr/bin/env bash

# Pinned provider compatibility contract shared by the ephemeral Remnawave fixture,
# acceptance reports, and public artifact validator.
readonly REMNAWAVE_PANEL_VERSION="3.4.5"
readonly REMNAWAVE_NODE_VERSION="3.4.2"
readonly REMNAWAVE_PANEL_IMAGE_DEFAULT="ghcr.io/remnawave/backend:${REMNAWAVE_PANEL_VERSION}@sha256:b16d724b90fd7c9fec2df04bd28938a671cafc62894105068e11550ee3449c56"
readonly REMNAWAVE_NODE_IMAGE_DEFAULT="ghcr.io/remnawave/node:${REMNAWAVE_NODE_VERSION}@sha256:1f97485b4bc7e4944f1ae95cc57d176376813b0022e9567b705f384f1a2e909d"
