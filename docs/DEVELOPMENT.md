# FlashDB Development Notes

## Debugging Log

### TTL Loading Bug (2026-09-08)

**Problem:** Keys without TTL were being skipped during Load.

**Root cause:** Incorrect condition for skipping expired keys.

**Wrong:**
```go
if !expiryTime.IsZero() && time.Now().After(expiryTime) {
    continue
}
```

**Correct:**
```go
if expiryUnix != 0 && time.Now().After(expiryTime) {
    continue
}
```

**Why:** 
- `expiryUnix == 0` means key has no TTL (permanent) → must load
- `expiryTime.IsZero()` checks if time is zero value, but `time.Unix(0, 0)` is 1970-01-01, not zero value
- We store `0` in file for "no expiry", so check the raw value before conversion

**Lesson:** Check the raw serialized value, not the converted type, when the raw value has semantic meaning (0 = no TTL).
