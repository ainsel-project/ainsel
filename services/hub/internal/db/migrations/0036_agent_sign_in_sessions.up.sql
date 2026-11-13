-- ChatGPT sign-in sessions (issue #89). A session relays exactly two values
-- between the console and an agent's short-lived sign-in pod: the OpenAI
-- authorization URL (posted by the pod) and the redirect URL the user pastes
-- back. Neither is a token: the authorization code in the redirect URL is
-- useless without the PKCE verifier that never leaves the pod, and the hub
-- clears redirect_url in the statement that hands it to the pod. Tokens are
-- never stored here.
CREATE TABLE agent_sign_in_sessions (
    id           TEXT        PRIMARY KEY,
    agent        TEXT        NOT NULL,
    state        TEXT        NOT NULL CHECK (state IN (
                     'waiting_for_pod', 'awaiting_redirect', 'exchanging',
                     'succeeded', 'failed', 'expired', 'cancelled')),
    auth_url     TEXT        NOT NULL DEFAULT '',
    redirect_url TEXT,
    error        TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL
);

-- One live session per agent: a second Start is a conflict, not a second pod.
CREATE UNIQUE INDEX agent_sign_in_sessions_one_live
    ON agent_sign_in_sessions (agent)
    WHERE state IN ('waiting_for_pod', 'awaiting_redirect', 'exchanging');

CREATE INDEX agent_sign_in_sessions_agent_created
    ON agent_sign_in_sessions (agent, created_at DESC);
