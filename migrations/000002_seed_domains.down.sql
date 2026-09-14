DELETE FROM assessment_domains WHERE slug IN (
    'network-security',
    'application-security',
    'logical-access-security',
    'data-security',
    'security-logging-and-monitoring',
    'change-performance-capacity-management',
    'cloud-security',
    'ai-security'
);
