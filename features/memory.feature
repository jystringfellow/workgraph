Feature: Active memory

Scenario: Initialize the local memory repo
  Given workgraph has not been initialized
  When I run "workgraph init"
  Then the local memory repo exists at "~/workgraph-memory"

Scenario: Preserve user-maintained memory files
  Given workgraph has already been initialized
  And the memory repo contains user-maintained files
  When I run "workgraph init"
  Then existing memory files are preserved

Scenario: Treat memory as user-owned context
  Given workgraph has access to local memory files
  When workgraph uses memory for context
  Then memory supports explanations and suggestions
  And captured events remain the source of truth for behavior

Scenario: Resume with project memory
  Given workgraph has captured events for a project
  And the memory repo contains Markdown memory for that project
  When I run "workgraph resume <project>"
  Then the output includes the project memory
  And the captured events remain visible as recent activity

Scenario: Resume initialized memory before captured activity
  Given workgraph has initialized project memory
  And workgraph has captured no events for that project
  When I run "workgraph resume <project>"
  Then the output includes the project memory
  And the output says no recent activity was found

Scenario: Point project resume at missing memory
  Given workgraph has captured events for a project
  And the memory repo has no Markdown memory for that project
  When I run "workgraph resume <project>"
  Then the output explains where to add project memory

Scenario: Initialize starter project memory
  Given workgraph has been initialized
  When I run "workgraph memory init <project>"
  Then project memory exists at a lower-kebab-case Markdown path
  And the memory contains starter headings for context, bets, decisions, people, and artifacts

Scenario: Initialize starter personal memory
  Given workgraph has been initialized
  When I run "workgraph memory init --scope personal"
  Then personal memory exists at "personal.md"
  And the memory contains starter headings for role, priorities, thinking models, voice, and AI collaboration

Scenario: Initialize starter organization memory
  Given workgraph has been initialized
  When I run "workgraph memory init --scope organization <organization>"
  Then organization memory exists at a lower-kebab-case Markdown path
  And the memory contains starter headings for strategic themes, operating principles, people, and priorities

Scenario: Initialize starter team memory
  Given workgraph has been initialized
  When I run "workgraph memory init --scope team <team>"
  Then team memory exists at a lower-kebab-case Markdown path
  And the memory contains starter headings for people, operating norms, rituals, and ownership

Scenario: Preserve existing project memory
  Given workgraph has been initialized
  And the project memory already exists
  When I run "workgraph memory init <project>"
  Then the existing project memory is preserved
  And the output reports its path

Scenario: Suggest project memory updates from evidence
  Given workgraph has captured events for a project
  When I run "workgraph memory suggest --scope project <project>"
  Then the output includes draft memory update suggestions
  And every suggestion includes event evidence
  And project memory is not created or modified

Scenario: Promote project memory from evidence
  Given workgraph has captured events for a project
  When I run "workgraph memory promote --scope project <project> --evidence <event-id> --text <memory text>"
  Then the memory text is appended to project memory
  And the promoted entry records the event evidence
  And workgraph stores a link from project memory to the event
  And existing project memory is preserved

Scenario: List project memory links
  Given project memory was promoted from event evidence
  When I run "workgraph memory links --scope project <project>"
  Then the output includes the project memory path
  And the output includes the linked event evidence

Scenario: Require base initialization for project memory
  Given workgraph has not been initialized
  When I run "workgraph memory init <project>"
  Then the output tells me to run "workgraph init"
