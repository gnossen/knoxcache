module github.com/gnossen/knoxcache

go 1.16

replace (
	github.com/gnossen/knoxcache/datastore => ./datastore
	github.com/gnossen/knoxcache/encoder => ./encoder
)

require golang.org/x/net v0.0.0-20210525063256-abc453219eb5
