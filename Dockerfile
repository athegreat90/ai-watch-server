# Runs only the Python monitoring API. AI CLI hooks stay on the developer's PC.
# Standard-library Python: no pip installations or build tools required.
FROM python:3.15.2-slim-bookworm@sha256:96599df822d0e1c6bb700014a05c7f3c8cabc6e0f7ff3c4f9f69cee033b4b5d9

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

RUN groupadd --system --gid 1000 aiwatch \
    && useradd --system --uid 1000 --gid aiwatch \
       --home-dir /nonexistent --no-create-home \
       --shell /usr/sbin/nologin aiwatch

COPY --chown=1000:1000 bridge/server.py bridge/healthcheck.py ./

USER 1000:1000
EXPOSE 8765

HEALTHCHECK --interval=30s --timeout=4s --start-period=10s --retries=3 \
    CMD ["python", "/app/healthcheck.py"]

CMD ["python", "/app/server.py"]
