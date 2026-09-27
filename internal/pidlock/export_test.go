package pidlock

// AcquireAs takes the lock on behalf of pid, so one test process can stand
// in for several racing ones.
var AcquireAs = acquire
