import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const apiTarget = process.env.GATEPILOT_API_TARGET ?? "http://127.0.0.1:8080";
const wsTarget = apiTarget.replace(/^http:/, "ws:").replace(/^https:/, "wss:");

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": apiTarget,
      "/ws": {
        target: wsTarget,
        ws: true
      }
    }
  }
});
