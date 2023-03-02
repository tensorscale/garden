## Setup

```
$ go install . 
$ garden serve
... localhost:7777 ...
```

migrations:

```
go install -tags 'sqlite3' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
./migrations/up.sh
```
