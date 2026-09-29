package sqlstore

// MaxBindParams exposes maxBindParams to the external sqlstore_test package,
// so the chunking tests can pick sizes that cross chunk boundaries.
const MaxBindParams = maxBindParams
