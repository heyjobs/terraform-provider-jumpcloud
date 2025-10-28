# Proof of Concept: User Groups Field

This document demonstrates the new `groups` field added to the `jumpcloud_user` resource, allowing you to manage a user's group memberships directly within the user resource.

## Overview

**Previous Approach (One resource per user-group association):**
```terraform
resource "jumpcloud_user" "john" {
  username = "john.doe"
  email    = "john.doe@acme.org"
}

resource "jumpcloud_user_group" "developers" {
  name = "Developers"
}

resource "jumpcloud_user_group" "admins" {
  name = "Admins"
}

# One membership resource per user-group pair
resource "jumpcloud_user_group_membership" "john_to_devs" {
  userid  = jumpcloud_user.john.id
  groupid = jumpcloud_user_group.developers.id
}

resource "jumpcloud_user_group_membership" "john_to_admins" {
  userid  = jumpcloud_user.john.id
  groupid = jumpcloud_user_group.admins.id
}
```

**New Approach (User-centric - Similar to Okta provider):**
```terraform
resource "jumpcloud_user" "john" {
  username = "john.doe"
  email    = "john.doe@acme.org"

  # All groups in one place
  groups = [
    jumpcloud_user_group.developers.id,
    jumpcloud_user_group.admins.id,
    jumpcloud_user_group.security.id,
  ]
}

resource "jumpcloud_user_group" "developers" {
  name = "Developers"
}

resource "jumpcloud_user_group" "admins" {
  name = "Admins"
}

resource "jumpcloud_user_group" "security" {
  name = "Security"
}
```

## Benefits

1. **Simpler Configuration**: All user information, including group memberships, is in one resource
2. **Easier to Reason About**: Looking at a user resource shows all their groups immediately
3. **Atomic Operations**: User and all their group memberships are managed together
4. **Less Resource Sprawl**: No need for multiple `jumpcloud_user_group_membership` resources

## Complete Example

```terraform
# Define groups
resource "jumpcloud_user_group" "engineering" {
  name = "Engineering"
  attributes = {
    department = "engineering"
  }
}

resource "jumpcloud_user_group" "frontend" {
  name = "Frontend Team"
  attributes = {
    team = "frontend"
  }
}

resource "jumpcloud_user_group" "backend" {
  name = "Backend Team"
  attributes = {
    team = "backend"
  }
}

# Create users with group memberships
resource "jumpcloud_user" "alice" {
  username   = "alice"
  email      = "alice@example.com"
  firstname  = "Alice"
  lastname   = "Smith"

  # Alice is in engineering and frontend groups
  groups = [
    jumpcloud_user_group.engineering.id,
    jumpcloud_user_group.frontend.id,
  ]
}

resource "jumpcloud_user" "bob" {
  username   = "bob"
  email      = "bob@example.com"
  firstname  = "Bob"
  lastname   = "Johnson"

  # Bob is in engineering and backend groups
  groups = [
    jumpcloud_user_group.engineering.id,
    jumpcloud_user_group.backend.id,
  ]
}

resource "jumpcloud_user" "charlie" {
  username  = "charlie"
  email     = "charlie@example.com"
  firstname = "Charlie"
  lastname  = "Williams"

  # Charlie is in all groups
  groups = [
    jumpcloud_user_group.engineering.id,
    jumpcloud_user_group.frontend.id,
    jumpcloud_user_group.backend.id,
  ]
}
```

## How It Works

### Schema Changes
- Added `groups` field to `jumpcloud_user` resource schema
- Type: `TypeSet` (unordered collection, no duplicates)
- Element type: `String` (group IDs)
- Optional field

### Implementation Details

#### Create Operation
1. User is created via v1 API
2. If `groups` field is specified, sync user to those groups via v2 API
3. Each group membership is added individually

#### Read Operation
1. User attributes are read via v1 API
2. User's group memberships are fetched via v2 API (`GraphUserAssociationsList`)
3. Group IDs are set in the `groups` field

#### Update Operation
1. User attributes are updated via v1 API
2. If `groups` field has changed:
   - Calculate which groups to add (new - old)
   - Calculate which groups to remove (old - new)
   - Sync memberships via v2 API

#### Delete Operation
- User deletion removes all associations automatically (no extra cleanup needed)

### Helper Functions Added

**`getUserGroupIDs(client, userID)`**
- Fetches all groups a user belongs to
- Uses pagination (100 items per page)
- Returns array of group IDs

**`syncUserGroups(client, userID, oldGroupIDs, newGroupIDs)`**
- Synchronizes user's group memberships
- Adds user to new groups
- Removes user from old groups
- Handles all API calls with proper error handling

## Migration Path

For existing users of `jumpcloud_user_group_membership`:

### Option 1: State Migration (Recommended)
```bash
# Remove old membership resources from state
terraform state rm jumpcloud_user_group_membership.john_to_devs
terraform state rm jumpcloud_user_group_membership.john_to_admins

# Update configuration to use new groups field
# Then run terraform apply
```

### Option 2: Gradual Migration
Keep both approaches during transition:
- New users: Use `groups` field
- Existing users: Continue using `jumpcloud_user_group_membership`
- Migrate when convenient

## Important Considerations

### Conflict Resolution
Using both methods simultaneously for the same user may cause conflicts:

**AVOID:**
```terraform
resource "jumpcloud_user" "john" {
  username = "john.doe"
  email    = "john.doe@acme.org"
  groups   = [jumpcloud_user_group.developers.id]  # Method 1
}

# Method 2 - conflicts with above!
resource "jumpcloud_user_group_membership" "john_to_admins" {
  userid  = jumpcloud_user.john.id
  groupid = jumpcloud_user_group.admins.id
}
```

### Group-Centric vs User-Centric
- **User-centric** (`groups` field): Best when managing individual users
- **Group-centric** (`jumpcloud_user_group.members`): Best when managing group composition
- **Membership resources**: Best for modular/reusable configurations

Choose the approach that fits your use case.

## Testing the POC

To test this implementation:

1. **Build the provider:**
   ```bash
   go build -o terraform-provider-jumpcloud
   ```

2. **Create a test configuration:**
   ```terraform
   terraform {
     required_providers {
       jumpcloud = {
         source = "local/jumpcloud"
       }
     }
   }

   provider "jumpcloud" {
     api_key = "your-api-key"
   }

   resource "jumpcloud_user_group" "test_group" {
     name = "Test Group POC"
   }

   resource "jumpcloud_user" "test_user" {
     username = "testuser.poc"
     email    = "testuser.poc@example.com"
     groups   = [jumpcloud_user_group.test_group.id]
   }
   ```

3. **Run Terraform:**
   ```bash
   terraform init
   terraform plan
   terraform apply
   ```

4. **Verify:**
   - Check JumpCloud console to confirm user is in the group
   - Run `terraform state show jumpcloud_user.test_user` to see the groups field
   - Modify groups and apply to test updates

## API Calls Overview

For a user with 3 groups:

**Create:**
- 1 call: Create user (v1 API)
- 3 calls: Add to each group (v2 API)
- 1 call: Read user groups for state refresh (v2 API)

**Read:**
- 1 call: Read user attributes (v1 API)
- ~1 call: List user's group associations (v2 API, paginated if >100 groups)

**Update (changing from 3 groups to 5 groups):**
- 1 call: Update user attributes (v1 API)
- 2 calls: Add to 2 new groups (v2 API)
- 1 call: Read user groups for state refresh (v2 API)

**Update (changing from 3 groups to 1 group):**
- 1 call: Update user attributes (v1 API)
- 2 calls: Remove from 2 old groups (v2 API)
- 1 call: Read user groups for state refresh (v2 API)

## Next Steps

To move this from POC to production:

1. **Add comprehensive tests** (`resource_user_test.go`)
2. **Update documentation** (update existing user resource docs)
3. **Add deprecation warnings** (if deprecating `jumpcloud_user_group_membership`)
4. **Handle edge cases** (very large group counts, API errors during partial sync)
5. **Add import support** (ensure groups are imported correctly)
6. **Performance optimization** (batch operations if API supports it)
