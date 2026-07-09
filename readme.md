# 📡 BinItRadar

BinItRadar is a decentralized, privacy-centric civic computing engine designed to crowdsource, categorize, and map unregulated urban garbage accumulation and public health hazards in real time. 

By combining a low-friction Telegram Ingress Bot with an automated Edge AI computer vision classifier and high-resolution geospatial boundary mapping, the system transforms citizen snapshots into structured, actionable operational data for municipal sanitation workflows.

---

## 🏗️ System Architecture

The platform is designed around an event-driven, decoupled pipeline that maximizes performance while minimizing data leakage and API costs.

[Telegram Client] ──(1. Photo)──> [Go Ingress Engine] ──(2. Batch Labels)──> [GCP Vision API]
│                                 │
│ (4. GPS Button Prompt)          ▼ (3. Write Sandbox Session)
└─── <─── <─── <─── <─── 📋 [MongoDB Atlas]
│                                 │
└──(5. Precision Lat/Lon) ───────>┤ (6. Promote GCS Asset + Resolve Geoapify Boundary)
▼
[Private GCS Bucket] ──(7. Local V4 Sign)──> [Vue.js Analytics]


1. **Ingress Layer (Telegram Bot):** Intercepts visual uploads, enforces automated anti-mischief rules, and buffers temporary sessions.
2. **Classification Layer (Google Cloud Vision API):** Runs a single-pass execution loop to extract primary infrastructure layouts (*regulated* vs. *unregulated*) along with specific hazard sub-types (*Plastics*, *Medical*, *Construction*, *Sanitary*).
3. **Geospatial Pipeline (Geoapify API):** Resolves precise device coordinates into multi-layered administrative boundaries (State, City, Suburb, Street) instead of generic point centroids.
4. **Storage & Security Sandbox (GCS + Mongo Partial TTL):** Unverified sessions are sandboxed with a 5-minute time-to-live (TTL) expiration window. Verified objects are promoted to long-term storage and served via **cryptographic V4 local pre-signed URLs** to ensure 100% bucket privacy.
5. **Operational Console (Vue.js 3 + Leaflet):** A reactive single-page dashboard featuring cyclical slide controls, reactive administrative filters, and real-time hover-interaction map pins.

---

## 📂 Project Directory Structure

```text
goFindGarbageDumps/
├── web/
│   └── index.html           # Vue.js 3 + Leaflet Dashboard Frontend
├── binitbot-key.json        # Google Cloud Service Account Credentials Key (Git ignored)
├── gcs-lifecycle.json       # GCS Staging Stale Object Deletion Ruleset
├── go.mod                   # Go Module dependencies
├── go.sum                   # Go checksum tracking
├── main.go                  # Core Go Application (Webhook + Analytics API Engine)
└── README.md                # System Documentation Manual