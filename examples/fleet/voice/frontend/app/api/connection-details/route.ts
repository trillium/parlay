import { AccessToken, AccessTokenOptions, VideoGrant } from "livekit-server-sdk";
import { NextResponse, NextRequest } from "next/server";

const API_KEY = process.env.LIVEKIT_API_KEY;
const API_SECRET = process.env.LIVEKIT_API_SECRET;

// ONE room, always the same name. With a random name per page load, every
// reload stranded the previous room and there was nothing stable to rejoin.
const ROOM = process.env.VOICE_ROOM || "voice_assistant_room";

// Where the BROWSER reaches LiveKit. Set VOICE_LIVEKIT_PUBLIC_URL to pin it;
// otherwise it follows the address the page itself was loaded from: through
// the TLS forwarder (https on :3443) the voice server is wss on :7443, and
// straight to the frontend (http on :3000, this Mac only) it is plain ws :7880.
const TLS_PAGE_PORT = process.env.VOICE_TLS_PAGE_PORT || "3443";
const TLS_LIVEKIT_PORT = process.env.VOICE_TLS_LIVEKIT_PORT || "7443";
const PLAIN_LIVEKIT_PORT = process.env.VOICE_LIVEKIT_PORT || "7880";

function livekitUrlFor(request: NextRequest): string {
  if (process.env.VOICE_LIVEKIT_PUBLIC_URL) return process.env.VOICE_LIVEKIT_PUBLIC_URL;
  const host = request.headers.get("host") || "localhost:3000";
  const m = /^(\[[^\]]+\]|[^:]+)(?::(\d+))?$/.exec(host);
  const name = (m && m[1]) || "localhost";
  const port = (m && m[2]) || "";
  return port === TLS_PAGE_PORT ? `wss://${name}:${TLS_LIVEKIT_PORT}` : `ws://${name}:${PLAIN_LIVEKIT_PORT}`;
}

// don't cache the results
export const revalidate = 0;
export const dynamic = "force-dynamic";

export type ConnectionDetails = {
  serverUrl: string;
  roomName: string;
  participantName: string;
  participantToken: string;
};

export async function GET(request: NextRequest) {
  try {
    // No password: tailnet membership is the access control (captain 2026-11-23).
    const url = new URL(request.url);
    // The page sends one identity per device so a reconnect replaces its old self.
    const asked = url.searchParams.get("identity") || "";
    const participantIdentity = /^[A-Za-z0-9_-]{1,64}$/.test(asked) && !asked.startsWith("voice-mode-bot")
      ? asked
      : `voice_assistant_user_${Math.floor(Math.random() * 10_000)}`;
    const participantToken = await createParticipantToken({ identity: participantIdentity }, ROOM);

    const data: ConnectionDetails = {
      serverUrl: livekitUrlFor(request),
      roomName: ROOM,
      participantToken,
      participantName: participantIdentity,
    };
    return NextResponse.json(data, { headers: new Headers({ "Cache-Control": "no-store" }) });
  } catch (error) {
    console.error(error);
    return new NextResponse(error instanceof Error ? error.message : "connection details failed", { status: 500 });
  }
}

function createParticipantToken(userInfo: AccessTokenOptions, roomName: string) {
  const at = new AccessToken(API_KEY, API_SECRET, {
    ...userInfo,
    ttl: "15m",
  });
  const grant: VideoGrant = {
    room: roomName,
    roomJoin: true,
    canPublish: true,
    canPublishData: true,
    canSubscribe: true,
  };
  at.addGrant(grant);
  return at.toJwt();
}
