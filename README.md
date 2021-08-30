Chisel
===

Chisel is a database frontend with support for transactions across multiple
databases and query transformations via jq. Its main use is as a basic JSON API
frontend to databases and for prototyping API work without the need to write
a great deal of boilerplate to get started.

Currently, Chisel is a work-in-progress and only its basic functionality is
implemented. This includes:

  * Listening on multiple addresses, and limiting endpoints to specific
    addresses.
  * Connecting to multiple databases for use in endpoints.
  * Defining HTTP endpoints, with support for both GET- and POST-style requests.
  * Executing a sequence of queries across one or more database transactions.
  * Parsing request query and path parameters using jq.
  * Mapping of database results, and passing database results to subsequent
    queries in transactions.

Installation
---

    $ go install go.spiff.io/chisel@latest

Chisel does not currently have version tags, so this will always build
and install the latest version to your GOBIN (defaults to `~/go/bin`).

To build it locally, you can use `bmake` or `pmake`, which will build
`chisel` with its default options as `bin/chisel`. This includes
additional SQLite options that are not present when using `go install`
on its own.

Otherwise, if you don't care for any of the default options, you can use
`go build` in the root of the project, which will produce a `chisel`
binary for you to run.

Usage
---

Chisel has very few CLI options currently, limited to the following:

Usage of chisel:
  * `-C` - Print the parsed program config as JSON and exit.
  * `-c=config.json` - The path to load program config JSON from.
    (default "config.json")
  * `-v=level` - Set the log level. May be one of `info` (default),
    `warn`, `error`, `fatal`, `panic`, `debug`, or `trace`.

Configuration
---

Chisel is configured using JSON or YAML. Example configuration below uses YAML
to provide comments alongside the data. The use of either is subject to change,
possibly to [codf][] later, so bear that in mind. Currently, the top-level
elements of a Chisel configuration are:

[codf]: https://go.spiff.io/codf

  * `bind` (`[]sockaddr`): A list of addresses and ports to listen for
    connections on. If none are given, it defaults to `127.0.0.1:8080` (over
    IPv4 only). Each address is assumed to be IPv4, IPv6, or a Unix domain
    socket path. Unix domain socket paths must begin with `/` or `.` to identify
    them as socket paths.

    ```yaml
    bind:
      - 127.0.0.1:8080    # IPv4 address and port.
      - '[::1]:8080'      # IPv6 address and port.
      - /var/run/chisel.s # Unix domain socket.
    ```

    Sockets in this list may be referred to by index later. Indices are
    zero-based, so `0` would refer to `127.0.0.1:8080` above, and `1` would
    refer to the Unix domain socket `/var/run/chisel.s`.

    This may change to provide named socket groups or treat all addresses as
    dual-stack where possible.

  * `databases` (`[string]database`): A mapping of database names to their
    configurations. See *Databases* below for the values these are configured
    with.

  * `endpoints` (`[]endpoint`): A list of endpoint definitions. See *Endpoints*
    below for the values these are configured with.

### Databases

Every database has a name and a URL. Beyond that, all other values for
a database are optional, and some are only configurable by setting values in the
URL (these options are DBMS-specific and handed off to the driver).

A fully-define database connection is provided below for reference:

```yaml
databases:
  test:
    url: sqlite://test.db # sqlite://, mysql://, postgres://
    # Connection limits:
    max_idle: 2      # Maximum idle connections.
    max_idle_time: 0 # Maximum idle connection lifespan.
    max_open: 0      # Maximum open connections.
    max_life_time: 0 # Maximum connection lifespan.
    # Query options:
    options:
      try_json: true       # Whether to try parsing values as JSON.
      skip_json: false     # Whether to skip all JSON parsing, even for JSON columns.
      time_format: rfc3339 # The format that times are parsed and rendered in.
      # Or:
      time_format: layout
      time_layout: '06.01.02.15.03.04.000000'
```

The following options from above are configurable:

  * `url` (`string`): A `postgres://`, `mysql://`, or `sqlite://`
    database URL. Only limited support is available for SQLite and
    MySQL. MySQL has the least support due to it lacking an
    [insert-returning][postgres-insert] statement, while Chisel's
    support for SQLite does not yet have locking and is experimental. If
    using MySQL, it is recommended you use MariaDB instead, which has
    support for an [insert-returning statement][mariadb-insert].

    The format of a database URL is as follows, with optional parts
    wrapped in square brackets:

    ```
    driver://[username:[password]@]hostname:port[/dbname][?options]
    ```

  * `max_idle` and `max_open` (`int`): These control max number of idle
    and open connections, respectively, for a database. By default, the
    maximum idle number is `2` and the maximum open is unlimited. These
    defaults are from Go, and may not always follow Go.

  * `max_idle_time` and `max_life_time` (`duration` string): These
    control the maximum lifespan of idle and open connections. By
    default, neither connection has a lifespan and will be open as long
    as is possible. These are formatted as Go duration strings, such as
    `5h4m3s2ms1us`.

  * `try_json` (`bool`): If true, Chisel will attempt to parse all
    retrieved database values as JSON where it looks like it can. This
    applies to all columns with a text-like type, not only those with
    a JSON column type. Defaults to false.

  * `skip_json` (`bool`): If true, Chisel will skip parsing JSON on all
    retrieved database values, including those with JSON column types.
    This overrides `try_json` and forces it to false. Defaults to false.

  * `time_format` (`enum`): Sets the format of times both parsed from
    the database and returned in JSON. Time values are not parsed from
    JSON in HTTP request bodies. May be one of the following values:
      - `rfc3339` (default) - format times as RFC 3339 date-time
        strings.
      - `fsec` - format times as Unix timestamps with sub-second
        fractional components.
      - `unixns` - format times as Unix timestamps in nanoseconds.
      - `unixus` - format times as Unix timestamps in microseconds.
      - `unixms` - format times as Unix timestamps in milliseconds.
      - `unix` - format times as Unix timestamps in seconds.
      - `layout` - format times using the layout string from
        `time_layout`.

  * `time_layout` (`string`): Sets the Go time layout string to parse
    and render times when `time_format` is set to `layout`.

[postgres-insert]: https://www.postgresql.org/docs/13/sql-insert.html
[mariadb-insert]: https://mariadb.com/kb/en/insertreturning/

### Endpoints

Endpoints define the HTTP endpoints served on one or more bind
addresses. An endpoint has the following top-level values:

  * `bind` (`[]int`): A set of one or more indices from the list of bind
    addresses. The endpoint will only be served on the addresses
    corresponding to the indices. The format of this field is subject to
    change, but may be used to limit certain endpoints to internal
    interfaces.

  * `method` (`string`, required): An HTTP method, such as `GET` or
    `POST`.

  * `path` (`string`, required): The HTTP path, rooted at `/`. You may
    define variable elements of the path by declaring them as `:name`,
    such as `/things/:id/name`, where `:id` is a path parameter name. To
    capture all subsequent path elements as a parameter, you can declare
    the final path element as `*name`, such as `/file/at/*path`, where
    all path elements after `/file/at/` are captured as the `path`
    parameter. Path routing is currently handled by [httprouter][], so
    its behavior determines how paths are currently handled.

  * `body_type` (`enum`): The type of body to expect if `METHOD` is not
    `GET` or `HEAD`. May be one of the following:
      - `json` (default): Parse request bodies as JSON. If parsing
        fails, reject the request.
      - `string`: Read the body without parsing it and treat it as
        a string.
      - `form`: Parse the body as a form. Currently unsupported.
      - `none`: Do not attempt to read or parse the request body.

  * `query_params`, `path_params` (`[string][]mapping`): Mappings for
    query and path parameters, respectively. Parameters named in this
    are transformed with one or more mappings, allowing you to parse
    parameters as numbers, check if they match a regexp, or other
    features using jq expressions. For example:

    ```yaml
    path_params:
      id:
        - tonumber | if . <= 0 then error("id must be a positive number") else . end
    ```

    Note: although you can pass multiple mappings per parameter, this
    may not be supported in the future.

  * `query` (`query`): Defines the query associated with the endpoint,
    including transactions and steps. See *Queries* below for more
    detail.

[httprouter]: https://github.com/julienschmidt/httprouter

### Queries

Queries have two top-level keys: `transactions`, which defines a list of
transactions that make up an endpoint's queries; and `steps`, which
defines a list of individual queries against those transactions and the
transformations and arguments to each.

#### Transactions

Transactions define one or more transactions for an endpoint when
accessing a database, as well as the isolation level of each
transaction. This is used to ensure that queries run by chisel have
appropriate isolation from other queries.

Every transaction defined in an endpoint must be used by at least one
step in the query.

```yaml
query:
  transactions:
  # 0
  - db: test
    isolation: serializable # Has a DBMS-level transaction.
  # 1
  - db: test
    isolation: none         # Has no DBMS-level transaction.
```

  * `db` (`string`): The list of transactions defines the databases that
    an endpoint accesses and the isolation level of each transaction
    against the database. The databases are referred to by their names
    in the root-level `databases` mapping.

  * `isolation` (`enum`): The isolation level of a transaction
    determines the kind of isolation the database gives the transaction.
    Not all databases are guaranteed to support all isolation levels (or
    even support them correctly if they do). Every database has its own
    default isolation level, and you should consult the DBMS
    documentation for yours.

    Valid isolation levels are:
      - `default` (default): Use the DBMS's default isolation level.
      - `none`: Do not create a transaction and instead run a one-off
        query against the database. This is useful for single-query
        endpoints and those that do not need to do things like perform
        multiple updates or inserts followed by selects.
      - `read_uncommitted`
	  - `read_committed`
	  - `write_committed`
	  - `repeatable_read`
	  - `snapshot`
	  - `serializable`
	  - `linearizable`

#### Steps

Query steps are the individual query statements, their arguments, and
the transformations applied to their results.

```yaml
query:
  steps:
  - transaction: 0
    query: SELECT * FROM builds WHERE id = ? LIMIT 1
    args:
    - path: id
    map: # Output is the first row.
    - first

  - transaction: 0
    query: SELECT id AS artifact_id, url AS artifact_url FROM artifacts WHERE build_id = ?
    args:
    # Fetch build ID from previous output.
    - expr: '$context.outputs[0][0].id'
    map: # Merge outputs.
    - '{ data: ({ artifacts: . } * $context.outputs[0]) }'
```

A step is defined by the following fields:

  * `transaction` (`int`): An index into the transactions list defined
    in the parent query. If not set, defaults to the first transaction
    as a convenience for single-transaction queries.

  * `query` (`string`, required): The query to run against the
    transaction. This can use `?` parameters as placeholders for
    anything the DBMS permits for parameterization. Because this uses
    [sqlx][] for parameter binding, cases like `col IN (?)` are expanded
    when list arguments (below) are given.

  * `args` (`[]arg`): The arguments passed to the above query. If the
    query doesn't take parameters, this must be empty or undefined.
    Each argument is defined in one of four ways:
    - A literal value, such as `1`, `"foo"`, or a list of literal values.
    - `{ path: "key" }` - A mapping binding the argument to the value of
      a path parameter, defined on the endpoint. If the parameter is not
      defined, the request fails.
    - `{ query: "key" }` - A mapping binding the argument to the value
      of a query parameter. As with path parameters, this must be
      defined for the request, or the request fails.
    - `{ expr: "jq" }` - A mapping binding the argument to the result
      value of a jq expression. Composite return types such as mappings
      are encoded as JSON, while lists are passed to the query for
      binding. To ensure that a list is encoded as JSON, the result of
      the expression should include a final `| tojson` pipeline. An
      example of this can be seen above in the second step's argument
      list.

  * `map` (`[]jqexpr`): A list of jq expressions, encoded as strings, to
    define transformations of the result set into the output of the
    query step. The output is captured and passed to the next steps for
    reuse. If this is the final step, the result of the mapping is used
    as the JSON response body.

    A special case is used to adjust the resulting HTTP status and
    headers on response, by returning an object of the form:

    ```json
    {
      "__response": {
        "status": 201,
        "headers": {
          "x-foobar": ["something"]
        },
        "data_key": "actual_body"
      },
      "actual_body": {
        "data": "foobar"
      }
    }
    ```

    Where, in the above case, all values of the `__response` object are
    optional. If `status` is undefined, it defaults to HTTP 200 (OK).

    If `data_key` is defined, instead of returning the object minus the
    `__response` key, the response body will be returned using the data
    found at the expression `.[$data_key]`. So, in the above example,
    the actual response returned in the response is `{"data":"foobar"}`.

[sqlx]: https://github.com/jmoiron/sqlx

License
---

Chisel is licensed under the Apache 2.0 license. A copy of this license
is included with the source code of the project.
