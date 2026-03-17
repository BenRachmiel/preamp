import { defineConfig } from "vite"
import preact from "@preact/preset-vite"
import tailwindcss from "@tailwindcss/vite"

export default defineConfig({
  plugins: [preact(), tailwindcss()],
  build: { target: "es2022" },
  server: {
    proxy: {
      "/admin": "http://localhost:4534",
      "/rest": "http://localhost:4533",
    },
  },
})
