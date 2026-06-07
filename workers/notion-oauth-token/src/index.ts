interface Env {
  NOTION_CLIENT_ID: string;
  NOTION_CLIENT_SECRET: string;
  NOTION_TOKEN_URL: string;
}

const DEFAULT_NOTION_CLIENT_ID = "378d872b-594c-8110-b4c0-0037422697b3";
const DEFAULT_NOTION_TOKEN_URL = "https://api.notion.com/v1/oauth/token";

const REQUIRED_FIELDS_BY_GRANT_TYPE: Record<string, string[]> = {
  authorization_code: [
    "grant_type",
    "code",
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
    if (request.method !== "POST" || url.pathname !== "/notion/token") {
      return json({ error: "not_found" }, 404);
    }

    const form = await readForm(request);
    if (!form) {
      return json({ error: "unsupported_media_type" }, 415);
    }

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

    const clientID = env.NOTION_CLIENT_ID || DEFAULT_NOTION_CLIENT_ID;
    if (form.get("client_id") !== clientID) {
      return json({ error: "invalid_client" }, 400);
    }
    if (!env.NOTION_CLIENT_SECRET) {
      return json({ error: "server_error", error_description: "Notion client secret is not configured" }, 500);
    }

    const notionResponse = await fetch(env.NOTION_TOKEN_URL || DEFAULT_NOTION_TOKEN_URL, {
      method: "POST",
      headers: {
        "Accept": "application/json",
        "Authorization": basicAuthorization(clientID, env.NOTION_CLIENT_SECRET),
        "Cache-Control": "no-store",
        "Content-Type": "application/json",
      },
      body: JSON.stringify(tokenRequestBody(form, grantType)),
    });

    return new Response(await notionResponse.text(), {
      status: notionResponse.status,
      headers: {
        "content-type": notionResponse.headers.get("content-type") || "application/json; charset=utf-8",
        "cache-control": "no-store",
      },
    });
  },
};

async function readForm(request: Request): Promise<URLSearchParams | null> {
  const contentType = request.headers.get("content-type") || "";
  const normalized = contentType.toLowerCase();
  if (normalized.includes("application/x-www-form-urlencoded")) {
    return new URLSearchParams(await request.text());
  }
  if (normalized.includes("application/json")) {
    const body = await request.json<Record<string, unknown>>();
    const form = new URLSearchParams();
    for (const [key, value] of Object.entries(body)) {
      if (typeof value === "string") {
        form.set(key, value);
      }
    }
    return form;
  }
  return null;
}

function tokenRequestBody(form: URLSearchParams, grantType: string): Record<string, string> {
  if (grantType === "authorization_code") {
    return {
      grant_type: grantType,
      code: form.get("code") || "",
      redirect_uri: form.get("redirect_uri") || "",
    };
  }
  return {
    grant_type: grantType,
    refresh_token: form.get("refresh_token") || "",
  };
}

function basicAuthorization(clientID: string, clientSecret: string): string {
  return `Basic ${btoa(`${clientID}:${clientSecret}`)}`;
}

function json(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
    },
  });
}
