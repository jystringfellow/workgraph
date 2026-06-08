# Azure Boards

Azure Boards should be a read-only work tracking connector for workgraph.

The first slice uses the same Microsoft OAuth PKCE pattern as Microsoft mail and
calendar, stores tokens locally, and asks for the Azure DevOps organization,
project, optional team, and optional area path filters during connection. Those
values let workgraph construct Azure DevOps Work Item Tracking URLs without
requiring the user to repeat provider details during routine capture.

Initial commands:

```text
workgraph azure boards connect --organization <org> --project <project> --team <team> --area-path <area-path>
workgraph azure boards capture
workgraph azure boards disconnect
```

`workgraph azure boards connect --no-browser` prints the Microsoft authorization
URL, state, and PKCE verifier for test/debug flows. Normal browser connection
uses `http://localhost:2727/azure/boards/callback`.

Successful connection writes `azure-boards.json` under the workgraph home with:

- access and refresh tokens
- organization
- project
- optional team
- optional area paths
- optional custom WIQL
- optional API/token base URLs for tests or non-default environments

Once connected, `workgraph start` polls Azure Boards by default under connector
id `azure.boards`. Users can inspect or change that polling with:

```text
workgraph connectors list
workgraph connectors disable azure.boards
workgraph connectors interval azure.boards 15m
```

The connector should use Azure DevOps Work Item Tracking REST APIs:

- `POST https://dev.azure.com/{organization}/{project}/_apis/wit/wiql?api-version=7.1`
- then fetch details for returned ids using the work item batch API

The WIQL query should be configurable. A practical default is active work for
the current user, ordered by changed date. When multiple area paths are
configured, they are combined as an OR group:

```sql
SELECT [System.Id]
FROM WorkItems
WHERE [System.TeamProject] = @Project
  AND [System.AssignedTo] = @Me
  AND [System.State] <> 'Closed'
  AND [System.State] <> 'Removed'
  AND ([System.AreaPath] UNDER 'Work\Platform' OR [System.AreaPath] UNDER 'Work\DevEx')
ORDER BY [System.ChangedDate] DESC
```

Captured work items should store `azure_boards.work_item` events with:

- work item id and web/API URLs
- title, state, work item type, assigned user, changed date
- tags, iteration path, area path, priority when present
- raw fields JSON for provider-specific fields

The connector must be read-only for the first slice. It must not update states,
add comments, edit fields, assign work, or create links. Future write behavior
must follow suggest -> draft -> approve -> act.

Sources:

- Azure Boards WIQL syntax supports `@Me`, `@Project`, and date macros.
- Azure DevOps Query By WIQL returns work item ids, after which callers fetch
  full work item fields separately.
