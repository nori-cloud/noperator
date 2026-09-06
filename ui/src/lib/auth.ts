import { betterAuth } from "better-auth";
import { Env } from "@/lib/system";

export const auth = betterAuth({
  secret: Env.Auth.BetterAuthSecret,
  baseURL: Env.Auth.BetterAuthUrl,
  session: {
    cookieCache: {
      enabled: true,
      maxAge: 60 * 60 * 24 * 7,
    },
  },
  socialProviders: {
    github: {
      clientId: Env.Auth.GitHubSSOID,
      clientSecret: Env.Auth.GitHubSSOSecret,
    },
  },
});
