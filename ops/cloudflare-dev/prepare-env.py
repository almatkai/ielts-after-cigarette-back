#!/usr/bin/env python3
"""Prepare private dev configuration on the SSH host, without printing secrets."""
import json
import pathlib
import secrets
import subprocess

root = pathlib.Path('/home/almat/ielts-guest-dev')
root.mkdir(mode=0o700, exist_ok=True)
source = json.loads(subprocess.check_output([
    'docker', 'inspect', 'ielts-api-production-backend-1'
]))[0]
production = dict(item.split('=', 1) for item in source['Config']['Env'] if '=' in item)
infra_path = root / 'dev.env'
if infra_path.exists():
    infra = dict(line.split('=', 1) for line in infra_path.read_text().splitlines() if '=' in line)
else:
    infra = {
        'POSTGRES_PASSWORD': secrets.token_hex(32),
        'REDIS_PASSWORD': secrets.token_hex(32),
        'MINIO_ROOT_USER': 'ielts-dev',
        'MINIO_ROOT_PASSWORD': secrets.token_hex(32),
    }
    infra_path.write_text(''.join(f'{k}={v}\n' for k, v in infra.items()))
infra_path.chmod(0o600)

# Keep the existing assessment providers and their encryption key. Auth,
# Redis, database and object-store credentials are independent of production.
keys = {
    'AI_API_KEY', 'AI_CHAT_COMPLETIONS_URL', 'AI_MODEL', 'AI_SPEAKING_MODEL',
    'AI_SPEAKING_AUDIO_ENABLED', 'AI_TIMEOUT', 'OPENROUTER_API_KEY',
    'OPENROUTER_MODEL', 'OPENROUTER_SPEAKING_MODEL', 'OPENROUTER_TIMEOUT',
    'STT_API_KEY', 'STT_API_URL', 'AI_PROVIDER_ENCRYPTION_KEY',
    'AI_PROVIDER_PREVIOUS_ENCRYPTION_KEY', 'GOOGLE_CLIENT_ID', 'SUPER_ADMIN_EMAILS',
}
app = {k: v for k, v in production.items() if k in keys and v}
app.update({
    'APP_ENV': 'development', 'HTTP_ADDR': ':8080', 'DB_MAX_CONNS': '10',
    'DATABASE_URL': f"postgres://ielts_dev:{infra['POSTGRES_PASSWORD']}@postgres:5432/ielts_dev?sslmode=disable",
    'REDIS_URL': f"redis://:{infra['REDIS_PASSWORD']}@redis:6379/0",
    'JWT_SECRET': secrets.token_hex(32), 'JWT_ISSUER': 'ielts-dev-api',
    'JWT_AUDIENCE': 'ielts-dev-web', 'PHONE_VERIFICATION_SECRET': secrets.token_hex(32),
    'REFRESH_COOKIE_NAME': 'iac_dev_refresh', 'REFRESH_COOKIE_SECURE': 'true',
    'REFRESH_COOKIE_SAME_SITE': 'lax', 'CORS_ALLOWED_ORIGINS': 'https://ielts.woolet.cc',
    'INFOBIP_ENABLED': 'false', 'SPEECH_ENABLED': 'false',
    'AI_PROVIDER_TELEMETRY_OPTIONAL': 'true', 'WRITING_WORKER_EXTERNAL': 'true',
    'WRITING_WORKER_ENABLED': 'true', 'SPEAKING_WORKER_ENABLED': 'true',
    'GUEST_TRIAL_ENABLED': 'true', 'GUEST_TRIAL_IP_LIMIT': '3',
    'GUEST_TRIAL_GLOBAL_LIMIT': '20',
    'GUEST_TRIAL_EXAM_TYPES': 'academic',
    'OBJECT_STORAGE_BACKEND': 'minio', 'OBJECT_STORAGE_ENDPOINT': 'minio:9000',
    'OBJECT_STORAGE_USE_SSL': 'false', 'OBJECT_STORAGE_BUCKET': 'ielts-dev-media',
    'OBJECT_STORAGE_ACCESS_KEY': infra['MINIO_ROOT_USER'],
    'OBJECT_STORAGE_SECRET_KEY': infra['MINIO_ROOT_PASSWORD'],
})
app_path = root / 'app.env'
if app_path.exists():
    raise SystemExit('app.env already exists; refusing to rotate credentials on a running dev stack')
if any('\n' in v or '\r' in v for v in app.values()):
    raise SystemExit('Invalid multiline environment value')
app_path.write_text(''.join(f'{k}={v}\n' for k, v in app.items()))
app_path.chmod(0o600)
print('Independent dev credentials and assessment configuration saved privately')
