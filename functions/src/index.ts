import { region } from 'firebase-functions'
import { readFileSync } from 'fs'

export const currentVersion = region('asia-northeast1').https.onRequest((_, res) => {
  let error: any | undefined = undefined;
  let currentVersion: string | undefined = undefined;
  try {
    const buf = readFileSync('VERSION')
    currentVersion = buf.toString();
  } catch (e) {
    error = e
  }
  if (error !== undefined) {
    res.status(503);
    res.json({ error });
    return;
  }
  res.json({ currentVersion })
})
