// Sinh vector chéo cho Signer: node gen_vectors.js (chạy ngoại tuyến; kết quả được commit).
// Chữ ký đúng thuật toán của Medplum storage/presign.ts: createSign('sha256') trên "<GET|PUT> <url không có Signature>", base64,
// tham số Expires, Project (nếu có), rồi Signature, bằng URLSearchParams (giống searchParams.set).
const crypto = require('crypto');
const fs = require('fs');
const { privateKey, publicKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
const keyPem = privateKey.export({ type: 'pkcs8', format: 'pem' });
const pubPem = publicKey.export({ type: 'spki', format: 'pem' });
fs.writeFileSync('test-signing-key.pem', keyPem);
fs.writeFileSync('test-signing-key.pub.pem', pubPem);

function presign(base, id, ver, method, projectId, expires) {
  const result = new URL(`${base}/${id}/${ver}`);
  result.searchParams.set('Expires', String(expires));
  if (projectId) result.searchParams.set('Project', projectId);
  const toSign = `${method} ${result.toString()}`;
  const signer = crypto.createSign('sha256');
  signer.update(toSign);
  const sig = signer.sign(keyPem, 'base64');
  result.searchParams.set('Signature', sig);
  return { method, base, id, ver, project: projectId || '', expires, signed: toSign, url: result.toString() };
}
const base = 'https://api.example.com/storage';
const vectors = [
  presign(base, '4b1e6b7a-0f6a-4a37-9c1b-7a3f1c2d9e01', '9a2f3c4d-1111-4222-8333-944455556666', 'GET', '', 1900000000),
  presign(base, '4b1e6b7a-0f6a-4a37-9c1b-7a3f1c2d9e01', '9a2f3c4d-1111-4222-8333-944455556666', 'PUT', '', 1900000000),
  presign(base, 'abc', 'v1', 'GET', '0b3e1f2a-7c4d-4e5f-8a6b-1c2d3e4f5a6b', 1900000000),
  presign(base, 'abc', 'v1', 'PUT', '0b3e1f2a-7c4d-4e5f-8a6b-1c2d3e4f5a6b', 1900000000),
  presign('http://localhost:8103/storage', 'abc', 'v1', 'GET', '', 1900000000),
];
fs.writeFileSync('vectors.json', JSON.stringify(vectors, null, 2) + '\n');
