# Discord CLI

The language used to describe access to a person's Discord account and its messages.

## Language

**Account**:
A Discord user identity whose conversations and server memberships the person using the CLI has authorized it to access.
_Avoid_: Bot account, agent

**Agent**:
Software acting on the account owner's instructions through the CLI.
_Avoid_: Account, Discord bot

**Server**:
A Discord community containing channels and members.
_Avoid_: Guild in user-facing language

**Channel**:
A container within Discord. DMs, group DMs, and server text channels are channel kinds that contain messages.
_Avoid_: Room

**DM**:
A private, one-to-one message channel between two Discord accounts.
_Avoid_: Private server, group chat

**Group DM**:
A private message channel for a group of Discord accounts, separate from a server.
_Avoid_: Group server

**Message**:
A post authored by an account in a message-bearing channel.

**Attachment**:
A file attached to a Discord message, such as an image, video, or document.
_Avoid_: Embed

**Reaction**:
An account's emoji response attached to a message.
_Avoid_: Reply

**Reply**:
A message that references another message in the same channel.
_Avoid_: Reaction

**User token**:
A secret credential that authenticates access as a Discord user account.
_Avoid_: Bot token, password

**Desktop session**:
The account owner's existing sign-in in an installed Discord desktop client.
_Avoid_: CLI login, imported account

**Token override**:
A user-supplied token that selects an account instead of the automatically detected desktop session.
_Avoid_: Additional account, profile
