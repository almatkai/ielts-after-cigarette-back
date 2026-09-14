// Read-only browser smoke test. Requires isolated Chrome on port 9223 and dev
// frontend/backend on 3000/8080. Auth and attempts are fixtures; Reading text is
// fetched from the real API. All unrecognised API writes are blocked in Chrome.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parseEnv } from 'node:util';
import { createHmac, randomUUID } from 'node:crypto';

const env = { ...parseEnv(readFileSync('.env', 'utf8')), ...parseEnv(readFileSync('.env.dev', 'utf8')) };
const now = Math.floor(Date.now() / 1000);
const userId = randomUUID();
const enc = obj => Buffer.from(JSON.stringify(obj)).toString('base64url');
const unsigned = enc({ alg: 'HS256', typ: 'JWT' }) + '.' + enc({
  sub: userId, role: 'STUDENT', iss: env.JWT_ISSUER || 'ielts-api',
  aud: [env.JWT_AUDIENCE || 'ielts-web'], iat: now, exp: now + 300,
});
const token = unsigned + '.' + createHmac('sha256', env.JWT_SECRET).update(unsigned).digest('base64url');
async function realApi(path) {
  const response = await fetch('http://localhost:8080/api/v1/' + path, { headers: { Authorization: 'Bearer ' + token } });
  assert(response.ok, `Real API ${path}: ${response.status}`);
  return response.json();
}
const catalog = await realApi('reading/materials');
const summary = catalog.items.find(item => item.kind === 'TEST') ??
  catalog.items.find(item => item.slug === 'reading-kakapo-elms-stress-40');
assert(summary, 'Published Reading test fixture not found');
const material = await realApi('reading/materials/' + summary.id);
const readingQuestions = (material.passages?.length ? material.passages : [material])
  .flatMap(passage => passage.questionGroups).flatMap(group => group.questions);
assert.equal(readingQuestions.reduce((sum, question) => sum + question.points, 0), 40);
const time = new Date().toISOString();
const attempt = { id: randomUUID(), userId, materialType: 'reading', materialId: material.id,
  materialVersionId: randomUUID(), status: 'IN_PROGRESS', score: null, maxScore: null,
  band: null, startedAt: time, submittedAt: null };
const mock = { id: randomUUID(), slug: 'browser-only-fixture', status: 'PUBLISHED', revision: 1,
  examType: 'academic', title: 'Browser-only Full Mock', description: 'Read-only route fixture',
  durationMinutes: 165, listeningMaterialId: randomUUID(), readingMaterialId: material.id,
  writingMaterialId: randomUUID(), speakingMaterialId: randomUUID(), createdAt: time, updatedAt: time, publishedAt: time };
const session = { id: randomUUID(), mockTestId: mock.id, userId, status: 'IN_PROGRESS', currentSection: 2,
  startedAt: time, deadlineAt: new Date(Date.now() + 3600000).toISOString(), submittedAt: null,
  overallBand: null, mockTest: mock, sections: ['listening', 'reading', 'writing', 'speaking'].map((skill, index) => ({
    position: index + 1, skill, attempt: { ...attempt, id: skill === 'reading' ? attempt.id : randomUUID(),
      materialType: skill, status: index === 0 ? 'SUBMITTED' : 'IN_PROGRESS' },
  })) };

const target = await (await fetch('http://127.0.0.1:9223/json/new?about:blank', { method: 'PUT' })).json();
const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((resolve, reject) => { ws.addEventListener('open', resolve, { once: true }); ws.addEventListener('error', reject, { once: true }); });
let sequence = 0;
const pending = new Map();
const errors = [];
let sectionRequests = 0;
let mockDetailRequests = 0;
function call(method, params = {}) {
  const id = ++sequence;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 15000);
    pending.set(id, { resolve, reject, timer }); ws.send(JSON.stringify({ id, method, params }));
  });
}
async function intercept(event) {
  const path = new URL(event.request.url).pathname.replace('/api/v1/', '');
  let body;
  let status = 200;
  if (event.request.method === 'OPTIONS') { body = ''; status = 204; }
  else if (path === 'auth/refresh') body = { accessToken: token, tokenType: 'Bearer', expiresIn: 300,
    user: { id: userId, email: 'browser-review@example.invalid', displayName: 'Browser review', role: 'STUDENT',
      phone: null, examType: 'academic', currentBand: null, targetBand: null, examDate: null, timezone: 'UTC', createdAt: time, updatedAt: time } };
  else if (path === `reading/materials/${material.id}/attempts`) body = { attempt, material };
  else if (path === `attempts/${attempt.id}`) body = { attempt, answers: [] };
  else if (path === 'full-mocks') body = { items: [mock] };
  else if (path === `full-mocks/${mock.id}`) { body = mock; mockDetailRequests++; }
  else if (path === `full-mock-sessions/${session.id}`) body = session;
  else if (path === `full-mock-sessions/${session.id}/sections/2`) {
    body = { ...session.sections[1], material }; sectionRequests++;
  } else if (event.request.method !== 'GET') {
    return call('Fetch.failRequest', { requestId: event.requestId, errorReason: 'BlockedByClient' });
  } else return call('Fetch.continueRequest', { requestId: event.requestId });
  return call('Fetch.fulfillRequest', { requestId: event.requestId, responseCode: status,
    responseHeaders: [
      { name: 'Content-Type', value: 'application/json' },
      { name: 'Access-Control-Allow-Origin', value: 'http://localhost:3000' },
      { name: 'Access-Control-Allow-Credentials', value: 'true' },
      { name: 'Access-Control-Allow-Headers', value: 'authorization,content-type' },
      { name: 'Access-Control-Allow-Methods', value: 'GET,POST,PUT,OPTIONS' },
    ], body: Buffer.from(status === 204 ? '' : JSON.stringify(body)).toString('base64') });
}
ws.addEventListener('message', event => {
  const msg = JSON.parse(event.data);
  if (msg.id) {
    const item = pending.get(msg.id); if (!item) return;
    pending.delete(msg.id); clearTimeout(item.timer);
    if (msg.error) item.reject(new Error(JSON.stringify(msg.error))); else item.resolve(msg.result);
  } else if (msg.method === 'Fetch.requestPaused') intercept(msg.params).catch(e => {
    // Navigation can cancel a paused request before its fulfilment reaches Chrome.
    if (!String(e).includes('Invalid InterceptionId')) errors.push(String(e));
  });
  else if (msg.method === 'Runtime.exceptionThrown') errors.push(msg.params.exceptionDetails.text);
});
async function evaluate(expression) {
  const response = await call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (response.exceptionDetails) throw new Error(response.exceptionDetails.text);
  return response.result.value;
}
async function until(expression) {
  const deadline = Date.now() + 45000;
  while (Date.now() < deadline) {
    if (await evaluate(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 350));
  }
  throw new Error('Browser timeout: ' + (await evaluate('document.body.innerText')).slice(0, 800));
}
try {
  await call('Page.enable'); await call('Runtime.enable');
  await call('Fetch.enable', { patterns: [{ urlPattern: 'http://localhost:8080/api/v1/*' }] });
  await call('Page.navigate', { url: 'http://localhost:3000/app/dashboard/reading' });
  await until(`Array.from(document.querySelectorAll('a')).some(a => a.textContent.includes('Открыть текст'))`);
  await evaluate(`Array.from(document.querySelectorAll('a')).find(a => a.textContent.includes('Открыть текст')).click()`);
  await until(`document.body.innerText.includes('Вопрос 1 из 40') && document.querySelectorAll('input').length >= 3`);
  await evaluate(`Array.from(document.querySelectorAll('button')).find(b => b.textContent.trim() === 'Далее').click()`);
  await until(`document.body.innerText.includes('Вопрос 2 из 40')`);
  console.log(JSON.stringify({ readingOpens: true, url: await evaluate('location.pathname'), questions: 40,
    passages: material.passages?.length || 1, attemptIsFixture: true }));

  await call('Page.navigate', { url: 'http://localhost:3000/app/dashboard/full-mocks/' + mock.id });
  await until(`document.body.innerText.includes('Browser-only Full Mock')`);
  console.log(JSON.stringify({ fullMockStartOpens: await evaluate(`document.body.innerText.includes('Начать или продолжить')`), mockDetailRequests }));

  await call('Page.navigate', { url: `http://localhost:3000/app/exam/full-mock-sessions/${session.id}/sections/2` });
  await until(`document.body.innerText.includes('Секции идут строго по порядку') || document.body.innerText.includes('world’s only flightless parrot')`);
  console.log(JSON.stringify({ fullMockSectionOpens: await evaluate(`document.body.innerText.includes('world’s only flightless parrot')`), sectionRequests }));
  assert.deepEqual(errors, []);
} finally {
  await fetch('http://127.0.0.1:9223/json/close/' + target.id).catch(() => {});
  ws.close();
}
