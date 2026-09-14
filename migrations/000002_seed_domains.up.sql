-- Seed the 8 TPSA assessment domains. These are data, not an enum: rows may be
-- added, renamed or deactivated later without a schema change.
--
-- scrutiny_note is injected into the AI prompt to calibrate how strictly
-- answers in that domain are judged. It is editable per row.

INSERT INTO assessment_domains (name, slug, sort_order, scrutiny_note) VALUES
('Network Security', 'network-security', 1,
 'Focus on segmentation, perimeter and internal controls, firewall/ACL governance, remote access paths, and how boundaries are tested. Treat "industry standard firewalls are in place" as vague unless the answer names the control, its scope and its review cadence.'),

('Application Security', 'application-security', 2,
 'Focus on secure SDLC, code review, dependency and SAST/DAST coverage, penetration testing cadence and remediation SLAs. An answer that cites a tool without saying what it gates, how often it runs, or who fixes findings is only partially responsive.'),

('Logical Access Security', 'logical-access-security', 3,
 'Focus on identity lifecycle (joiner/mover/leaver), least privilege, privileged account handling, MFA coverage, and access recertification frequency. Watch for MFA claimed broadly but scoped only to one system, and for shared or service accounts left unaddressed.'),

('Data Security', 'data-security', 4,
 'Focus on classification, encryption at rest and in transit with named algorithms and key management, data residency, retention and secure disposal, and onward transfer to sub-processors. "Data is encrypted" without cipher, scope or key custody is not a complete answer.'),

('Security Logging and Monitoring', 'security-logging-and-monitoring', 5,
 'Focus on what is logged, where logs go, retention period, tamper resistance, alerting coverage and who monitors them and when. Distinguish between collecting logs and actually detecting on them; 24x7 coverage claims should say whether it is in-house or outsourced.'),

('Change, Performance and Capacity Management', 'change-performance-capacity-management', 6,
 'Focus on change approval and segregation of duties, emergency change handling, rollback, capacity forecasting, and availability/performance monitoring against stated SLAs. Look for changes that bypass review and for capacity answers with no thresholds or trigger points.'),

('Cloud Security', 'cloud-security', 7,
 'Apply elevated scrutiny. Focus on the shared responsibility split, tenancy model, cloud posture management, IAM and key management in the provider, exposure of storage and management planes, and provider region/sub-processor disclosure. A generic "our cloud provider is certified" answer does not address the vendor''s own configuration responsibilities.'),

('AI Security', 'ai-security', 8,
 'Apply elevated scrutiny; this area is newer and answers are frequently thin. Focus on whether customer data trains or is retained by models, model and prompt injection defences, third-party model and provider disclosure, human oversight of AI-driven decisions, output validation, and AI-specific governance. Absence of a stated policy is itself a finding, not a neutral answer.')
ON CONFLICT (slug) DO NOTHING;
