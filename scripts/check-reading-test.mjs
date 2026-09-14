// Read-only structure smoke check plus attempt start for an existing user.
// Node 22+. Usage: node scripts/check-reading-test.mjs <test-id> <user-id>
import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { parseEnv } from 'node:util';

const [testId, userId, action] = process.argv.slice(2);
assert.match(testId ?? '', /^[0-9a-f-]{36}$/i);
assert.match(userId ?? '', /^[0-9a-f-]{36}$/i);
const env = {
  ...parseEnv(readFileSync('.env', 'utf8')),
  ...parseEnv(readFileSync('.env.dev', 'utf8')),
};
const now = Math.floor(Date.now() / 1000);
const encode = value => Buffer.from(JSON.stringify(value)).toString('base64url');
const unsigned = `${encode({ alg: 'HS256', typ: 'JWT' })}.${encode({
  sub: userId, role: 'ADMIN', iss: env.JWT_ISSUER || 'ielts-api',
  aud: [env.JWT_AUDIENCE || 'ielts-web'], iat: now, exp: now + 300,
})}`;
const token = `${unsigned}.${createHmac('sha256', env.JWT_SECRET).update(unsigned).digest('base64url')}`;
async function request(path, init) {
  const response = await fetch(`http://localhost:8080/api/v1/${path}`, {
    ...init,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  });
  if (!response.ok) {
    throw new Error(`${path}: HTTP ${response.status} ${await response.text()}`);
  }
  return response.json();
}

const catalog = await request('reading/materials');
assert(catalog.items.some(item => item.id === testId && item.kind === 'TEST'));
const started = await request(`reading/materials/${testId}/attempts`, { method: 'POST' });
assert.equal(started.material.kind, 'TEST');
assert.equal(started.material.passages.length, 3);
const points = started.material.passages.flatMap(p => p.questionGroups)
  .flatMap(group => group.questions).reduce((sum, question) => sum + question.points, 0);
assert.equal(points, 40);
let submitted;
if (action === '--submit') {
  submitted = await request(`attempts/${started.attempt.id}/submit`, {
    method: 'POST', body: JSON.stringify({ answers: [] }),
  });
  assert.equal(submitted.maxScore, 40);
  assert.equal(submitted.score, 0);
}
console.log(JSON.stringify({ testId, passages: 3, points, attemptId: started.attempt.id,
  submitted: submitted ? { score: submitted.score, maxScore: submitted.maxScore } : false }));
