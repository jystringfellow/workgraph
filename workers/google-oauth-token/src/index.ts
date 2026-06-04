interface Env {
  GOOGLE_CLIENT_ID: string;
  GOOGLE_CLIENT_SECRET: string;
  GOOGLE_TOKEN_URL: string;
}

const REQUIRED_FIELDS_BY_GRANT_TYPE: Record<string, string[]> = {
  authorization_code: [
    "grant_type",
    "code",
    "code_verifier",
    "redirect_uri",
    "client_id",
  ],
  refresh_token: [
    "grant_type",
    "refresh_token",
    "client_id",
  ],
};

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    if (request.method !== "POST" || url.pathname !== "/calendar/google/token") {
      return json({ error: "not_found" }, 404);
    }

    const contentType = request.headers.get("content-type") || "";
    if (!contentType.toLowerCase().includes("application/x-www-form-urlencoded")) {
      return json({ error: "unsupported_media_type" }, 415);
    }

    const form = new URLSearchParams(await request.text());
    const grantType = form.get("grant_type") || "";
    const requiredFields = REQUIRED_FIELDS_BY_GRANT_TYPE[grantType];
    if (!requiredFields) {
      return json({ error: "unsupported_grant_type" }, 400);
    }
    for (const field of requiredFields) {
      if (!form.get(field)) {
        return json({ error: "invalid_request", error_description: `${field} is required` }, 400);
      }
    }
    if (form.get("client_id") !== env.GOOGLE_CLIENT_ID) {
      return json({ error: "invalid_client" }, 400);
    }
    if (!env.GOOGLE_CLIENT_SECRET) {
      return json({ error: "server_error", error_description: "Google client secret is not configured" }, 500);
    }

    form.set("client_secret", env.GOOGLE_CLIENT_SECRET);

    const googleResponse = await fetch(env.GOOGLE_TOKEN_URL || "https://oauth2.googleapis.com/token", {
      method: "POST",
      headers: {
        "content-type": "application/x-www-form-urlencoded",
        "cache-control": "no-store",
      },
      body: form.toString(),
    });

    return new Response(await googleResponse.text(), {
      status: googleResponse.status,
      headers: {
        "content-type": googleResponse.headers.get("content-type") || "application/json; charset=utf-8",
        "cache-control": "no-store",
      },
    });
  },
};

function json(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
    },
  });
}
