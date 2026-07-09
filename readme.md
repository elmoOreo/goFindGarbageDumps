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

🛠️ Infrastructure Configuration
1. MongoDB Sandbox Partial TTL Index
To configure the 5-minute automated cleanup sandbox for aborted or unverified reports, ensure your dumpSites collection has the following partial index topology deployed (this is automatically validated during initMongoDB() engine execution):

JavaScript
db.dumpSites.createIndex(
   { "recordedOn": 1 },
   { 
     expireAfterSeconds: 300,
     partialFilterExpression: { "city": "Unverified" }
   }
)
2. Private Google Cloud Storage Configurations
Keep your target bucket strictly closed to public tracking (allUsers blocked). Ensure that Cross-Origin Resource Sharing (CORS) is enabled so your local or static bucket dashboard can safely download pre-signed binary chunks.

Create a gcs-cors.json file:

JSON
[
  {
    "origin": ["*"],
    "method": ["GET", "HEAD"],
    "responseHeader": ["Content-Type", "Access-Control-Allow-Origin"],
    "maxAgeSeconds": 3600
  }
]
Push the ruleset to your production footprint:

Bash
gcloud storage buckets update gs://YOUR_PRODUCTION_BUCKET_NAME --cors-file=gcs-cors.json
🚀 Local Runtime Ingress Deployment
Export your environmental variables inside your deployment terminal session:

Bash
# Google Cloud Platform Authentication Hook
export GOOGLE_APPLICATION_CREDENTIALS="binitbot-key.json"

# Core Application Properties
export BUCKET_NAME="your-gcs-bucket-name"
export MONGODB_URI="mongodb+srv://<user>:<password>@cluster.mongodb.net/?retryWrites=true&w=majority"
export TELEGRAM_APITOKEN="1234567890:ABCdefGhIJKlmNoPQRsTUVwxyZ"
export GEOAPIFY_KEY="your-geoapify-api-token-key"
export PORT="8080"
Compile and boot up your Go backend infrastructure:

Bash
# Fetch required modules
go mod tidy

# Execute runtime loop
go run main.go
The Go engine will spin up, verify connection pools to your MongoDB Atlas cluster, validate index bounds, and begin serving endpoints on :8080:

POST /webhook - Entry gateway hook for incoming Telegram actions.

GET /api/reports - Secure analytics reporting filter loop providing dynamically signed V4 asset tokens.

📱 User Interaction Manifesto
/start Command (Onboarding)
Fires automatically when a user joins the channel. Explains the operational vision ("Why we exist") and sets crisp expectations on how the report pipeline operates.

/help Command (Data Standards Guidelines)
Delivers a highly compressed, viewport-optimized layout calibrated to fit an iPhone 12 screen frame without scrolling.

Expected Inputs: Clear photos of municipal waste layers and rapid location sharing within a 5-minute window.

Prohibited Inputs: Plain text conversation, chatter, queries, or non-garbage image assets.

🛡️ Automated Enforcement & Blocklist Firewall
If a user attempts to chat using plain text or uploads non-conforming images (falsified telemetry files with zero garbage features matched by the Google Vision label array), the engine short-circuits:

It registers the user's chatId signature permanently into the blockedUsers collection.

It completely deletes the non-conforming file from Google Cloud Storage staging folders immediately.

It drops all future processing requests from that user, returning a static warning message: Contact administrator to unblock.