// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import "./lib/theme";
import App from "./App";
import { createTransport } from "./lib/transport";
import { start } from "./lib/store";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

void createTransport().then(start);
