# Amazon S3 — Complete Guide for AWS SAA-C03

> S3 is directly or indirectly tested in roughly 15–25% of SAA-C03 questions. This guide covers every concept you need: fundamentals, storage classes, security, data management, performance, and common exam scenarios.

---

## 1. S3 Fundamentals

**What S3 is:** Object storage — not block storage (EBS) and not a file system (EFS). You store objects (files + metadata) inside **buckets**.

Key facts to memorize:

| Property | Detail |
|---|---|
| Bucket names | Globally unique, DNS-compliant, lowercase, 3–63 chars, no underscores |
| Object key | Full path/filename, up to 1024 bytes |
| Object size | 0 bytes to 5 TB |
| Single PUT limit | 5 GB (use multipart upload above 100 MB, required above 5 GB) |
| Region | Buckets are created in a specific region; data stays there unless replicated |
| Consistency | **Strong read-after-write consistency** for all operations (PUTs, GETs, LISTs) — since Dec 2020, no more "eventual consistency" caveat |
| Durability | 11 nines (99.999999999%) across **all** storage classes |
| Availability | Varies by storage class (99.5%–99.99%) |
| Flat structure | No real folders — "folders" are just key prefixes (e.g. `photos/2024/img.jpg`) |

**Object metadata:** system metadata (Content-Type, ETag, Last-Modified) + up to 2 KB of user-defined custom metadata (`x-amz-meta-*`).

**Exam tip:** S3 is *regional* (not edge/global) unless you pair it with CloudFront or enable Multi-Region Access Points / Cross-Region Replication.

---

## 2. Storage Classes

This is the single most tested S3 subtopic. Know the access pattern, minimum storage duration, retrieval fee, and AZ redundancy for each.

| Storage Class | Use Case | Availability | Min. Duration | Retrieval Fee | AZs |
|---|---|---|---|---|---|
| **S3 Standard** | Frequently accessed, general purpose | 99.99% | None | None | ≥3 |
| **S3 Intelligent-Tiering** | Unknown/changing access patterns | 99.9% | None | None (small monitoring fee/object) | ≥3 |
| **S3 Standard-IA** | Infrequent access, needs millisecond retrieval | 99.9% | 30 days | Per-GB retrieval fee | ≥3 |
| **S3 One Zone-IA** | Infrequent, re-creatable data | 99.5% | 30 days | Per-GB retrieval fee | 1 |
| **S3 Glacier Instant Retrieval** | Archive accessed ~quarterly, millisecond access | 99.9% | 90 days | Higher retrieval fee | ≥3 |
| **S3 Glacier Flexible Retrieval** | Archive, retrieval in minutes–hours | 99.99% | 90 days | Retrieval fee (Expedited/Standard/Bulk tiers) | ≥3 |
| **S3 Glacier Deep Archive** | Long-term archive (7–10 yr retention), rarely accessed | 99.99% | 180 days | Retrieval in 12–48 hrs | ≥3 |
| **S3 Express One Zone** | Latency-sensitive workloads needing single-digit ms access | 99.95% | None | N/A (different pricing model) | 1 |

### Intelligent-Tiering access tiers (automatic, no retrieval fee)
1. Frequent Access
2. Infrequent Access (after 30 days no access)
3. Archive Instant Access (after 90 days)
4. Archive Access (optional, 90–700+ days)
5. Deep Archive Access (optional, 180–700+ days)

### Glacier retrieval speed tiers
| Tier | Flexible Retrieval | Deep Archive |
|---|---|---|
| Expedited | 1–5 min | N/A |
| Standard | 3–5 hrs | 12 hrs |
| Bulk | 5–12 hrs | 48 hrs |

**Exam tip:** "Data accessed unpredictably, want automatic cost savings, no operational overhead" → **Intelligent-Tiering**. "Data can be recreated / non-critical, single AZ is fine" → **One Zone-IA**. "Compliance archive, access once a year, cost is #1 priority" → **Glacier Deep Archive**.

---

## 3. Lifecycle Policies

Automate transitioning and expiring objects to save cost. Two rule types:

- **Transition actions** — move objects to a cheaper class after N days (e.g., Standard → Standard-IA after 30 days → Glacier after 90 days).
- **Expiration actions** — delete objects (or old versions) after N days.

Rules can apply to:
- Current versions
- Previous (noncurrent) versions — critical when versioning is enabled, to avoid unbounded storage cost
- Incomplete multipart uploads (clean up abandoned uploads to save cost)

**Valid transition order (enforced by AWS):**
```
Standard → Standard-IA / Intelligent-Tiering → One Zone-IA → Glacier IR → Glacier Flexible → Glacier Deep Archive
```
You can't skip backward, and minimum storage durations apply before transitioning (e.g., can't move to Standard-IA before 30 days).

**Exam tip:** Lifecycle rules + versioning is a classic scenario — "reduce cost of old versions" = transition/expire noncurrent versions.

---

## 4. Versioning

- Off by default. Once **enabled**, cannot be fully disabled — only **suspended**.
- Every PUT to the same key creates a new version with a unique Version ID.
- A DELETE without a version ID adds a **delete marker** (soft delete) — object appears gone but is recoverable by removing the marker.
- Deleting a specific version ID is a **permanent** hard delete.
- **MFA Delete** — extra protection requiring MFA to permanently delete a version or change versioning state. Only the bucket owner (root) can enable/disable it, via CLI only.
- Versioning is a **prerequisite** for Cross-Region Replication and for S3 Object Lock.

---

## 5. Replication (CRR & SRR)

| Type | What | Common Use Case |
|---|---|---|
| **CRR** (Cross-Region Replication) | Replicate objects to a bucket in a different region | Compliance, latency reduction for global users, disaster recovery |
| **SRR** (Same-Region Replication) | Replicate objects to a bucket in the same region | Log aggregation, prod → test account sync, compliance requiring redundant copies |

Requirements:
- **Versioning must be enabled** on both source and destination buckets.
- Replication is **not retroactive** — only replicates new objects after the rule is created (unless you use S3 Batch Replication for existing objects).
- Can replicate across AWS accounts.
- Delete markers: by default not replicated (configurable).
- Chained replication (replica → another replica) is not automatic unless you explicitly enable it.

---

## 6. Security & Access Control

### 6.1 Default state
All S3 buckets and objects are **private** by default. Access is denied unless explicitly granted.

### 6.2 Access control mechanisms

| Mechanism | Scope | Notes |
|---|---|---|
| **IAM Policies** | Attached to users/roles | Controls what an identity can do across AWS, including S3 |
| **Bucket Policies** | Attached to the bucket | JSON resource policy; can grant cross-account access; can allow/deny by condition (IP, VPC, encryption, etc.) |
| **ACLs (Access Control Lists)** | Bucket or object level | Legacy, AWS recommends disabling ACLs (bucket owner enforced) and using policies instead |
| **S3 Block Public Access** | Account or bucket level | Overrides ACLs/policies to prevent public access — enabled by default on new buckets |

**Evaluation logic:** An explicit **Deny** anywhere always wins. Otherwise access needs an explicit **Allow** from IAM policy, bucket policy, or ACL.

### 6.3 Pre-signed URLs
Grant temporary, time-limited access to a private object using the credentials of the URL-generator (upload or download) without changing bucket permissions. Common exam scenario: "let a mobile app user upload directly to S3 without exposing IAM credentials."

### 6.4 Encryption

| Type | Description |
|---|---|
| **SSE-S3** | Server-side, AWS-managed keys (AES-256) — default for new buckets |
| **SSE-KMS** | Server-side, AWS KMS-managed keys — auditability via CloudTrail, control over key policy, can hit KMS API request limits at scale |
| **SSE-C** | Server-side, customer-provided keys — you manage the key, AWS never stores it |
| **Client-Side Encryption** | Encrypt before upload; AWS never sees plaintext |
| **In-transit** | Enforced via bucket policy `aws:SecureTransport` condition to require HTTPS |

**Exam tip:** "High-volume workload hitting throttling on encryption" → SSE-S3 (no API call limit) instead of SSE-KMS. "Need audit trail of who used the encryption key" → SSE-KMS.

### 6.5 VPC Endpoints for S3
Allows EC2/services inside a VPC (with no internet gateway/NAT) to reach S3 privately.
- **Gateway Endpoint** — free, route-table based, S3 and DynamoDB only.
- **Interface Endpoint (PrivateLink)** — ENI with private IP, costs money, supports more services, needed if accessing from on-prem via Direct Connect/VPN.

### 6.6 S3 Object Lock (WORM)
Write Once Read Many — prevents object deletion/overwrite for a fixed retention period or indefinitely (legal hold). Requires versioning. Two modes:
- **Governance mode** — users with special permission can override.
- **Compliance mode** — no one, including root, can override until retention expires.

---

## 7. Performance

- **Multipart Upload** — splits large objects into parts uploaded in parallel; required for objects >5 GB, recommended above 100 MB. Improves throughput and allows resuming failed uploads.
- **S3 Transfer Acceleration** — routes uploads through CloudFront edge locations over optimized AWS backbone to speed up long-distance transfers into a bucket.
- **Byte-Range Fetches** — parallelize downloads or retrieve only part of an object (also used for resuming failed downloads or fetching partial file headers).
- **Request rate** — S3 automatically scales to support very high request rates (3,500 PUT/COPY/POST/DELETE and 5,500 GET/HEAD per second, *per prefix*) — spreading load across key prefixes increases effective throughput.
- **S3 Express One Zone** — purpose-built for single-digit millisecond latency, high request-rate workloads (e.g., ML training data, high-frequency analytics).

---

## 8. Data Management & Access Tools

| Feature | Purpose |
|---|---|
| **S3 Select / Glacier Select** | Retrieve only the data you need from an object using SQL, instead of pulling the whole object — reduces cost/latency |
| **S3 Batch Operations** | Run an action (copy, tag, restore, invoke Lambda) across billions of existing objects in one job |
| **S3 Inventory** | Scheduled CSV/ORC/Parquet report listing objects and metadata — alternative to slow LIST calls on huge buckets |
| **S3 Storage Lens** | Organization-wide visibility into storage usage and activity trends, with recommendations |
| **S3 Access Analyzer** | Identifies buckets shared with external entities |
| **Event Notifications** | Trigger Lambda, SQS, or SNS on object events (e.g., ObjectCreated) — common for image-processing pipelines |
| **S3 Object Tags** | Key-value tags used for lifecycle rule filtering, access control conditions, and cost allocation |

---

## 9. Static Website Hosting

- S3 can host a static website (HTML/CSS/JS, no server-side code).
- Bucket must allow public read access (or be fronted by CloudFront with Origin Access Control).
- You get an S3 website endpoint (`bucket-name.s3-website-region.amazonaws.com`).
- Common exam pattern: **S3 (static site) + CloudFront (CDN/HTTPS) + Route 53 (custom domain)** + ACM for TLS cert.
- Errors: configure an index document and a custom error document (e.g., for SPA routing, redirect 404s to index.html).

---

## 10. S3 vs. Other Storage — Exam Comparison Table

| | S3 | EBS | EFS | FSx |
|---|---|---|---|---|
| Type | Object storage | Block storage | File storage (NFS) | Managed file systems (Windows/Lustre) |
| Attach point | Accessed via API/HTTPS, not "mounted" to one instance | Attached to a single EC2 instance (unless Multi-Attach io1/io2) | Mounted by many EC2 instances concurrently, Multi-AZ | Mounted by many instances |
| Scaling | Virtually unlimited | Fixed size, resizable | Elastic, auto-scales | Elastic |
| Use case | Static assets, backups, data lake, logs | Boot volumes, databases | Shared content, CMS, big data workloads needing POSIX FS | Windows file shares, HPC (Lustre) |

---

## 11. Common SAA-C03 Scenario Patterns

- **Cost optimization for unpredictable access data** → Intelligent-Tiering
- **Static website with global low-latency access + HTTPS** → S3 + CloudFront + ACM + Route 53
- **Compliance: must not allow deletion for 7 years** → Object Lock, Compliance mode
- **Disaster recovery across regions** → Versioning + CRR
- **Large file uploads from users worldwide** → Transfer Acceleration or multipart upload
- **Query only part of a large CSV/JSON/Parquet object** → S3 Select or Athena (for ad-hoc SQL over many objects)
- **Analytics over huge S3 data lake with standard SQL, no infrastructure** → Athena + S3, or Redshift Spectrum for integration with a Redshift warehouse
- **Migrate on-prem file data into S3 continuously** → AWS DataSync or Storage Gateway (File Gateway)
- **One-time huge data migration (petabytes, poor network)** → AWS Snowball / Snowball Edge / Snowmobile
- **Grant a mobile app temporary upload access without IAM creds** → Pre-signed URL
- **Serve private content to select users via CloudFront** → Signed URLs/Cookies + Origin Access Control
- **Reduce storage cost from many object versions piling up** → Lifecycle rule on noncurrent versions
- **Prevent accidental public exposure across the whole account** → S3 Block Public Access at account level

---

## 12. Quick Reference — Numbers to Memorize

- Max object size: **5 TB**
- Max single PUT: **5 GB**
- Multipart required above: **5 GB** (recommended >100 MB)
- Durability: **99.999999999%** (11 nines) — all classes
- Standard-IA / One Zone-IA minimum duration: **30 days**
- Glacier Instant Retrieval minimum duration: **90 days**
- Glacier Deep Archive minimum duration: **180 days**
- Standard availability: **99.99%**
- One Zone-IA availability: **99.5%**
- Bucket name length: **3–63 characters**
- Custom metadata limit: **2 KB**

---

## 13. Suggested Study Flow

1. Storage classes + lifecycle policies (highest exam weight)
2. Versioning + replication (CRR/SRR)
3. Security: bucket policies vs IAM vs ACLs, Block Public Access, encryption types
4. Performance features: multipart upload, transfer acceleration, prefixes
5. Static website hosting + CloudFront integration
6. Data management tools: S3 Select, Batch Operations, Inventory, Storage Lens
7. Do scenario-based practice questions — S3 rarely appears in isolation; it's usually combined with CloudFront, Lambda, KMS, or Glacier in a full architecture question.
