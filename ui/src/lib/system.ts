export const Env = {
  System: {
    IsDev: process.env.NODE_ENV === "development",
    NodeEnv: process.env.NODE_ENV ?? "development",
    LogLevel: process.env.LOG_LEVEL ?? "info",
  },
  Auth: {
    BetterAuthUrl:
      process.env.BETTER_AUTH_URL ?? "https://noperator.hoki-sole.ts.net",
    BetterAuthSecret: process.env.BETTER_AUTH_SECRET ?? "",
    GitHubSSOID: process.env.GITHUB_CLIENT_ID ?? "",
    GitHubSSOSecret: process.env.GITHUB_CLIENT_SECRET ?? "",
  },
};
