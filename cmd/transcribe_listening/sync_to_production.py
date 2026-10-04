#!/usr/bin/env python3
"""
Syncs all published Listening tests, transcripts, and question timestamps
from alem.ai PostgreSQL to production PostgreSQL on 78.40.109.172.
"""

import os
import sys
import json
import subprocess
from pathlib import Path
from urllib.parse import urlsplit, unquote, parse_qsl

BACKEND_DIR = Path('/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back')
ENV_FILE = BACKEND_DIR / '.env'
PROD_AUTHOR_ID = 'ced4eb8b-c0b2-4215-be9e-ab491a4d3f56'

def get_alem_env():
    lines = ENV_FILE.read_text().splitlines()
    d = {}
    for line in lines:
        if line and not line.startswith('#') and '=' in line:
            k, v = line.split('=', 1)
            d[k.strip()] = v.strip().strip('"').strip("'")
    return d

def run_alem_query(query, env_dict):
    u = urlsplit(env_dict['DATABASE_URL'])
    env = os.environ.copy()
    env['PGPASSWORD'] = unquote(u.password or '')
    env['PGSSLMODE'] = dict(parse_qsl(u.query)).get('sslmode', 'prefer')
    env['PGCONNECT_TIMEOUT'] = '10'
    cmd = [
        'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-tA',
        '-h', u.hostname, '-p', str(u.port or 5432),
        '-U', unquote(u.username or ''), '-d', u.path.lstrip('/')
    ]
    r = subprocess.run(cmd, input=query, text=True, capture_output=True, env=env)
    if r.returncode != 0:
        raise RuntimeError(f"Alem query failed:\n{r.stderr}")
    return r.stdout

def run_prod_sql(sql):
    cmd = [
        'ssh', '-p', '9865', '-o', 'BatchMode=yes',
        'almat@78.40.109.172',
        'docker exec -i ielts-api-production-postgres-1 psql -U ielts_app -d ielts'
    ]
    r = subprocess.run(cmd, input=sql, text=True, capture_output=True)
    if r.returncode != 0:
        raise RuntimeError(f"Prod query failed:\n{r.stderr}\nOutput:\n{r.stdout}")
    return r.stdout

def escape_sql_str(val):
    if val is None:
        return 'NULL'
    # Escape single quotes and backslashes
    escaped = str(val).replace("'", "''")
    return f"'{escaped}'"

def escape_json(val):
    if val is None:
        return 'NULL'
    s = json.dumps(val)
    escaped = s.replace("'", "''")
    return f"'{escaped}'::jsonb"

def sync_tests(test_ids=None):
    alem_env = get_alem_env()
    
    where_clause = ""
    if test_ids:
        ids_str = ", ".join(f"'{tid}'" for tid in test_ids)
        where_clause = f"AND t.id IN ({ids_str})"

    # Fetch tests to sync
    query = f"""
        SELECT json_build_object(
            'test', json_build_object(
                'id', t.id,
                'slug', t.slug,
                'exam_type', t.exam_type,
                'status', t.status,
                'revision', t.revision,
                'current_version_id', t.current_version_id,
                'published_version_id', t.published_version_id,
                'published_at', t.published_at
            ),
            'version', json_build_object(
                'id', v.id,
                'test_id', v.test_id,
                'version_number', v.version_number,
                'title', v.title,
                'description', v.description,
                'duration_minutes', v.duration_minutes
            ),
            'parts', (
                SELECT json_agg(json_build_object(
                    'id', p.id,
                    'test_version_id', p.test_version_id,
                    'position', p.position,
                    'title', p.title,
                    'audio_asset_id', p.audio_asset_id,
                    'transcript', p.transcript,
                    'transcript_segments', p.transcript_segments
                ) ORDER BY p.position)
                FROM listening_parts p
                WHERE p.test_version_id = v.id
            ),
            'question_groups', (
                SELECT json_agg(json_build_object(
                    'id', g.id,
                    'part_id', g.part_id,
                    'position', g.position,
                    'question_type', g.question_type,
                    'instructions', g.instructions,
                    'context', g.context,
                    'config', g.config,
                    'image_asset_id', g.image_asset_id
                ) ORDER BY g.position)
                FROM listening_parts p
                JOIN listening_question_groups g ON g.part_id = p.id
                WHERE p.test_version_id = v.id
            ),
            'questions', (
                SELECT json_agg(json_build_object(
                    'id', q.id,
                    'group_id', q.group_id,
                    'position', q.position,
                    'number', q.number,
                    'prompt', q.prompt,
                    'content', q.content,
                    'answer', q.answer,
                    'explanation', q.explanation,
                    'points', q.points
                ) ORDER BY q.number)
                FROM listening_parts p
                JOIN listening_question_groups g ON g.part_id = p.id
                JOIN listening_questions q ON q.group_id = g.id
                WHERE p.test_version_id = v.id
            )
        )::text
        FROM listening_tests t
        JOIN listening_test_versions v ON v.id = t.published_version_id
        WHERE t.published_version_id IS NOT NULL {where_clause}
        ORDER BY v.title;
    """

    out = run_alem_query(query, alem_env)
    lines = [l.strip() for l in out.splitlines() if l.strip()]
    print(f"Loaded {len(lines)} published test(s) from alem.ai.")

    sql_statements = ["BEGIN;"]
    
    for line in lines:
        data = json.loads(line)
        test = data['test']
        version = data['version']
        parts = data.get('parts') or []
        groups = data.get('question_groups') or []
        questions = data.get('questions') or []

        # 1. Version
        sql_statements.append(f"""
            INSERT INTO listening_test_versions (
                id, test_id, version_number, title, description, duration_minutes, created_by, created_at
            ) VALUES (
                {escape_sql_str(version['id'])},
                {escape_sql_str(version['test_id'])},
                {version['version_number']},
                {escape_sql_str(version['title'])},
                {escape_sql_str(version['description'])},
                {version['duration_minutes']},
                {escape_sql_str(PROD_AUTHOR_ID)},
                NOW()
            )
            ON CONFLICT (id) DO UPDATE SET
                version_number = EXCLUDED.version_number,
                title = EXCLUDED.title,
                description = EXCLUDED.description,
                duration_minutes = EXCLUDED.duration_minutes;
        """)

        # 2. Parts
        for p in parts:
            sql_statements.append(f"""
                INSERT INTO listening_parts (
                    id, test_version_id, position, title, audio_asset_id, transcript, transcript_segments, created_at
                ) VALUES (
                    {escape_sql_str(p['id'])},
                    {escape_sql_str(p['test_version_id'])},
                    {p['position']},
                    {escape_sql_str(p['title'])},
                    {escape_sql_str(p['audio_asset_id'])},
                    {escape_sql_str(p['transcript'])},
                    {escape_json(p['transcript_segments'])},
                    NOW()
                )
                ON CONFLICT (id) DO UPDATE SET
                    position = EXCLUDED.position,
                    title = EXCLUDED.title,
                    audio_asset_id = EXCLUDED.audio_asset_id,
                    transcript = EXCLUDED.transcript,
                    transcript_segments = EXCLUDED.transcript_segments;
            """)

        # 3. Question Groups
        for g in groups:
            sql_statements.append(f"""
                INSERT INTO listening_question_groups (
                    id, part_id, position, question_type, instructions, context, config, image_asset_id, created_at
                ) VALUES (
                    {escape_sql_str(g['id'])},
                    {escape_sql_str(g['part_id'])},
                    {g['position']},
                    {escape_sql_str(g['question_type'])},
                    {escape_sql_str(g['instructions'])},
                    {escape_sql_str(g['context'])},
                    {escape_json(g['config'])},
                    {escape_sql_str(g['image_asset_id'])},
                    NOW()
                )
                ON CONFLICT (id) DO UPDATE SET
                    position = EXCLUDED.position,
                    question_type = EXCLUDED.question_type,
                    instructions = EXCLUDED.instructions,
                    context = EXCLUDED.context,
                    config = EXCLUDED.config,
                    image_asset_id = EXCLUDED.image_asset_id;
            """)

        # 4. Questions
        for q in questions:
            sql_statements.append(f"""
                INSERT INTO listening_questions (
                    id, group_id, position, number, prompt, content, answer, explanation, points, created_at
                ) VALUES (
                    {escape_sql_str(q['id'])},
                    {escape_sql_str(q['group_id'])},
                    {q['position']},
                    {q['number']},
                    {escape_sql_str(q['prompt'])},
                    {escape_json(q['content'])},
                    {escape_json(q['answer'])},
                    {escape_sql_str(q['explanation'])},
                    {q['points'] or 1},
                    NOW()
                )
                ON CONFLICT (id) DO UPDATE SET
                    position = EXCLUDED.position,
                    number = EXCLUDED.number,
                    prompt = EXCLUDED.prompt,
                    content = EXCLUDED.content,
                    answer = EXCLUDED.answer,
                    explanation = EXCLUDED.explanation,
                    points = EXCLUDED.points;
            """)

        # 5. Update listening_tests
        pub_at = escape_sql_str(test['published_at']) if test['published_at'] else 'NOW()'
        sql_statements.append(f"""
            UPDATE listening_tests
            SET current_version_id = {escape_sql_str(test['current_version_id'])},
                published_version_id = {escape_sql_str(test['published_version_id'])},
                revision = {test['revision']},
                status = 'PUBLISHED',
                published_at = {pub_at},
                updated_at = NOW()
            WHERE id = {escape_sql_str(test['id'])};
        """)

        # 6. Update student attempts
        sql_statements.append(f"""
            UPDATE attempts
            SET material_version_id = {escape_sql_str(test['published_version_id'])}
            WHERE material_type = 'listening'
              AND material_id = {escape_sql_str(test['id'])};
        """)

    sql_statements.append("COMMIT;")
    full_sql = "\n".join(sql_statements)

    print(f"Applying sync to production database on 78.40.109.172 ({len(lines)} tests, {len(sql_statements)} statements)...")
    res = run_prod_sql(full_sql)
    print("Production response:", res.strip().splitlines()[-1] if res.strip() else "OK")

    # Verify counts on production
    verify_sql = """
        SELECT v.title,
               COUNT(CASE WHEN length(coalesce(p.transcript, '')) > 0 THEN 1 END) as parts_with_transcript,
               COUNT(CASE WHEN q.content ? 'timestampStart' THEN 1 END) as questions_with_timestamps
        FROM listening_tests t
        JOIN listening_test_versions v ON v.id = t.published_version_id
        JOIN listening_parts p ON p.test_version_id = v.id
        LEFT JOIN listening_question_groups g ON g.part_id = p.id
        LEFT JOIN listening_questions q ON q.group_id = g.id
        GROUP BY t.id, v.title
        ORDER BY v.title;
    """
    prod_summary = run_prod_sql(verify_sql)
    print("\nVerified published tests on PRODUCTION:")
    print(prod_summary.strip())

if __name__ == '__main__':
    sync_tests()
