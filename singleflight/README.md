# singleflight

singleflight is a well known pattern.
If multiple goroutines need to do the same expensive operation,
it's more efficient to do it once and use the same result by all participants.
This library solves exactly that.

It's well similar to [golang.org/x/sync/singleflight](golang.org/x/sync/singleflight),
but written at the time of type parameters being available,
and it also avoids allocations.

## How it works

First goroutine enters the group and starts doing the heavy operation.
If another goroutine calls the same group while the first one is still working,
it blocks until result is available.
Then all the participating goroutines receive the same result and error.
Panic is also propagated.

## Usage

### Group

Single group is suitable if all the workers need the same work done.
Refreshing a common state for example.
If different workes may need a different work done use KeyedGroups (see below).

```go
// shared between workers
var g singleflight.Group[Res] // result type parameter used by workers

func doWork() (Res, error) {
    res, err := g.Do(func() (Res, error) {
        // do heavy lifting
        res, err := goToAnotherServerOrIntoDB()

        // res must be readable in parallel by few workers.
        // that means you can't return http.Response here,
        // as its Body only readable once.
        // Instead, read body here and return []byte
        // or parse into a struct and return it.

        return res, err
    })
    if err != nil {
        // ...
    }

    return res, nil // all workers get the same result for the price of one.
}
```

### KeyedGroups

KeyedGroups is suitable if multiple different resources need to be used,
but still the same resource is likely to be accesses by multiple goroutines at once.
It could be making requests to a limited set of resources.
```go
// shared by all workers
var g singleflight.KeyedGroups[Key, Res] // key is a type of resource identifier, res is a type of result

func doWorkForResource(resource Key) (Res, error) {
    res, err := g.Do(resource, func() (Res, error) {
        // This function is called once per unique resource in parallel.

        // do heavy lifting
        res, err := goToAnotherServerOrIntoDB(resource) // the difference from the Group is here.

        // same as for Group

        return res, err
    })
    if err != nil {
        // ...
    }

    return res, nil // all workers get the same result for the price of one.
}
```
