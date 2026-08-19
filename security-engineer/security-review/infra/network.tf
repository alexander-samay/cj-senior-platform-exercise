data "aws_vpc" "forecasting" {
  tags = {
    Name = "forecasting-vpc"
  }
}

data "aws_subnets" "private" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.forecasting.id]
  }

  filter {
    name   = "tag:Tier"
    values = ["private"]
  }
}

resource "aws_security_group" "forecasting_api_alb" {
  name        = "forecasting-api-alb"
  description = "Security group for forecasting API internal ALB"
  vpc_id      = data.aws_vpc.forecasting.id

  ingress {
    description = "HTTPS from clients"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description     = "Forward traffic to forecasting API pods"
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.forecasting_api_pods.id]
  }

  tags = {
    Service = "forecasting-api"
  }
}

resource "aws_lb" "forecasting_api" {
  name               = "forecasting-api-internal"
  internal           = true
  load_balancer_type = "application"
  security_groups    = [aws_security_group.forecasting_api_alb.id]
  subnets            = data.aws_subnets.private.ids
}

resource "aws_security_group" "forecasting_api_pods" {
  name        = "forecasting-api-pods"
  description = "Forecasting API pod ingress"
  vpc_id      = data.aws_vpc.forecasting.id
}

resource "aws_security_group_rule" "api_from_alb" {
  type                     = "ingress"
  security_group_id        = aws_security_group.forecasting_api_pods.id
  source_security_group_id = aws_security_group.forecasting_api_alb.id
  from_port                = 8080
  to_port                  = 8080
  protocol                 = "tcp"
  description              = "API traffic from ALB"
}

resource "aws_security_group" "forecast_worker_pods" {
  name        = "forecast-worker-pods"
  description = "Forecast worker pod security group"
  vpc_id      = data.aws_vpc.forecasting.id
}
