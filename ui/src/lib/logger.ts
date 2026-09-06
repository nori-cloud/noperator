import pino from "pino";
import { Env } from "@/lib/system";

export const logger = pino({
  level: Env.System.LogLevel,
  base: { service: "noperator-ui", env: Env.System.NodeEnv },
  transport: Env.System.IsDev
    ? { target: "pino-pretty", options: { colorize: true } }
    : undefined,
});
