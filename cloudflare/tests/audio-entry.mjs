export { IACAudio } from "../runtime/audio-worker.mjs";
export default {
  async fetch(request, env) {
    return Response.json(
      await env.IAC_AUDIO.get(
        env.IAC_AUDIO.idFromName("ielts-audio-dev-v1"),
      ).convert(await request.text()),
    );
  },
};
